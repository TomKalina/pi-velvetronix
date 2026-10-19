package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeELF returns a minimal ELF64 header that debug/elf parses.
func fakeELF(class byte, data byte, machine uint16, typ uint16) []byte {
	h := make([]byte, 64)
	copy(h, "\x7FELF")
	h[4] = class
	h[5] = data
	h[6] = 1 // EI_VERSION
	var bo binary.ByteOrder = binary.LittleEndian
	if data == 2 {
		bo = binary.BigEndian
	}
	bo.PutUint16(h[16:], typ)
	bo.PutUint16(h[18:], machine)
	bo.PutUint32(h[20:], 1)  // e_version
	bo.PutUint16(h[52:], 64) // e_ehsize
	bo.PutUint16(h[54:], 56) // e_phentsize
	bo.PutUint16(h[58:], 64) // e_shentsize
	return append(h, bytes.Repeat([]byte{0}, 128)...)
}

var aarch64 = fakeELF(2, 1, 183, 2)

type entry struct {
	name string
	typ  byte
	data []byte
}

func manifestFor(bin []byte, mod func(m map[string]any)) []byte {
	sum := sha256.Sum256(bin)
	m := map[string]any{"name": "pi-velvetronix", "version": "0.1.1", "arch": "linux/arm64",
		"binary": "pi-velvetronix", "sha256": hex.EncodeToString(sum[:]), "built": "2026-10-19T10:00:00Z"}
	if mod != nil {
		mod(m)
	}
	b, _ := json.Marshal(m)
	return b
}

func targz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o755}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.data))
		}
		if typ == tar.TypeSymlink {
			hdr.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write(e.data)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func pkg(t *testing.T, bin []byte, mod func(m map[string]any)) []byte {
	return targz(t, entry{name: "manifest.json", data: manifestFor(bin, mod)}, entry{name: "pi-velvetronix", data: bin})
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "untyped: " + err.Error()
}

func TestValidateOK(t *testing.T) {
	p, err := Validate(bytes.NewReader(pkg(t, aarch64, nil)), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.Version != "0.1.1" || !bytes.Equal(p.binary, aarch64) {
		t.Errorf("package = %+v", p.Manifest)
	}
	// ET_DYN (PIE) is accepted as well.
	if _, err := Validate(bytes.NewReader(pkg(t, fakeELF(2, 1, 183, 3), nil)), "0.1.0"); err != nil {
		t.Errorf("ET_DYN: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	man := manifestFor(aarch64, nil)
	xz := append([]byte{0xFD, '7', 'z', 'X', 'Z', 0x00}, bytes.Repeat([]byte{1}, 100)...)
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, _ := zw.Create("pi-velvetronix")
	w.Write(aarch64)
	zw.Close()
	x86 := fakeELF(2, 1, 62, 2)
	arm32 := fakeELF(1, 1, 40, 2)
	bigEndian := fakeELF(2, 2, 183, 2)
	relocatable := fakeELF(2, 1, 183, 1)
	notELF := []byte("#!/bin/sh\necho hi\n")
	badGzip := pkg(t, aarch64, nil)
	badGzip[len(badGzip)-6] ^= 0xFF // corrupt the CRC32 trailer

	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"empty", nil, CodeNotGzip},
		{"xz image", xz, CodeNotGzip},
		{"raw img", bytes.Repeat([]byte{0}, 512), CodeNotGzip},
		{"zip", zipBuf.Bytes(), CodeNotGzip},
		{"bare ELF", aarch64, CodeNotGzip},
		{"gzip but not tar", gzipOf([]byte("hello world, not a tar archive")), CodeBadTar},
		{"bad gzip checksum", badGzip, CodeBadTar},
		{"directory", targz(t, entry{name: "bin/", typ: tar.TypeDir}), CodeUnexpectedEntry},
		{"symlink", targz(t, entry{name: "pi-velvetronix", typ: tar.TypeSymlink}), CodeUnexpectedEntry},
		{"traversal", targz(t, entry{name: "../pi-velvetronix", data: aarch64}), CodeUnexpectedEntry},
		{"dot slash prefix", targz(t, entry{name: "./manifest.json", data: man}), CodeUnexpectedEntry},
		{"extra entry", targz(t, entry{name: "manifest.json", data: man}, entry{name: "pi-velvetronix", data: aarch64}, entry{name: "README", data: []byte("x")}), CodeUnexpectedEntry},
		{"duplicate", targz(t, entry{name: "manifest.json", data: man}, entry{name: "manifest.json", data: man}), CodeUnexpectedEntry},
		{"manifest too large", targz(t, entry{name: "manifest.json", data: bytes.Repeat([]byte(" "), maxManifest+1)}), CodeTooLarge},
		{"missing manifest", targz(t, entry{name: "pi-velvetronix", data: aarch64}), CodeMissingEntry},
		{"missing binary", targz(t, entry{name: "manifest.json", data: man}), CodeMissingEntry},
		{"bad json", targz(t, entry{name: "manifest.json", data: []byte("{nope")}, entry{name: "pi-velvetronix", data: aarch64}), CodeBadManifest},
		{"bad sha field", pkg(t, aarch64, func(m map[string]any) { m["sha256"] = "abc" }), CodeBadManifest},
		{"bad binary field", pkg(t, aarch64, func(m map[string]any) { m["binary"] = "other" }), CodeBadManifest},
		{"wrong name", pkg(t, aarch64, func(m map[string]any) { m["name"] = "other" }), CodeWrongName},
		{"wrong name before arch", pkg(t, aarch64, func(m map[string]any) { m["name"] = "x"; m["arch"] = "linux/amd64" }), CodeWrongName},
		{"wrong arch", pkg(t, aarch64, func(m map[string]any) { m["arch"] = "linux/amd64" }), CodeWrongArch},
		{"bad version chars", pkg(t, aarch64, func(m map[string]any) { m["version"] = "0.1/../x" }), CodeBadVersion},
		{"bad version empty", pkg(t, aarch64, func(m map[string]any) { m["version"] = "" }), CodeBadVersion},
		{"bad version dotdot", pkg(t, aarch64, func(m map[string]any) { m["version"] = ".." }), CodeBadVersion},
		{"bad version dot", pkg(t, aarch64, func(m map[string]any) { m["version"] = "." }), CodeBadVersion},
		{"bad version dot file", pkg(t, aarch64, func(m map[string]any) { m["version"] = ".request.tmp" }), CodeBadVersion},
		{"bad version request", pkg(t, aarch64, func(m map[string]any) { m["version"] = "request" }), CodeBadVersion},
		{"bad version rollback", pkg(t, aarch64, func(m map[string]any) { m["version"] = "rollback" }), CodeBadVersion},
		{"bad version long", pkg(t, aarch64, func(m map[string]any) { m["version"] = strings.Repeat("1", 65) }), CodeBadVersion},
		{"same version", pkg(t, aarch64, func(m map[string]any) { m["version"] = "0.1.0" }), CodeSameVersion},
		{"sha mismatch", targz(t, entry{name: "manifest.json", data: man}, entry{name: "pi-velvetronix", data: x86}), CodeSHA256Mismatch},
		{"not elf", pkg(t, notELF, nil), CodeNotELF},
		{"x86-64", pkg(t, x86, nil), CodeNotAArch64},
		{"arm32", pkg(t, arm32, nil), CodeNotAArch64},
		{"big endian", pkg(t, bigEndian, nil), CodeNotAArch64},
		{"relocatable", pkg(t, relocatable, nil), CodeNotAArch64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(bytes.NewReader(tt.body), "0.1.0")
			if got := code(err); got != tt.want {
				t.Errorf("code = %q (%v), want %q", got, err, tt.want)
			}
		})
	}
}

func gzipOf(b []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(b)
	gz.Close()
	return buf.Bytes()
}

func TestValidateTooLarge(t *testing.T) {
	oldBin, oldPkg := maxBinary, maxPackage
	t.Cleanup(func() { maxBinary, maxPackage = oldBin, oldPkg })

	big := make([]byte, 8<<10)
	rand.Read(big) // incompressible
	body := pkg(t, append(append([]byte(nil), aarch64...), big...), nil)

	maxBinary, maxPackage = 4<<10, 1<<20
	if _, err := Validate(bytes.NewReader(body), "0.1.0"); code(err) != CodeTooLarge {
		t.Errorf("binary over limit: %v", err)
	}
	maxBinary, maxPackage = 1<<20, 4<<10
	if _, err := Validate(bytes.NewReader(body), "0.1.0"); code(err) != CodeTooLarge {
		t.Errorf("package over limit: %v", err)
	}
	maxBinary, maxPackage = 1<<20, 1<<20
	if _, err := Validate(bytes.NewReader(body), "0.1.0"); err != nil {
		t.Errorf("within limits: %v", err)
	}
}

// useDir points Dir at a fresh temp dir for one test.
func useDir(t *testing.T) string {
	old := Dir
	t.Cleanup(func() { Dir = old })
	Dir = t.TempDir()
	return Dir
}

func TestStage(t *testing.T) {
	useDir(t)
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "inbox"), 0o750)
	p, err := Validate(bytes.NewReader(pkg(t, aarch64, nil)), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Stage(state); err != nil {
		t.Fatal(err)
	}
	bin, _ := os.ReadFile(filepath.Join(state, "inbox", "0.1.1", "pi-velvetronix"))
	man, _ := os.ReadFile(filepath.Join(state, "inbox", "0.1.1", "manifest.json"))
	req, _ := os.ReadFile(filepath.Join(state, "inbox", "request"))
	if !bytes.Equal(bin, aarch64) || !bytes.Equal(man, p.manifest) || string(req) != "0.1.1\n" {
		t.Fatalf("staged bin=%d man=%q req=%q", len(bin), man, req)
	}
	if _, err := os.Stat(filepath.Join(state, "inbox", ".request.tmp")); err == nil {
		t.Error("temp request left behind")
	}
	entries, _ := os.ReadDir(filepath.Join(state, "inbox", "0.1.1"))
	if len(entries) != 2 {
		t.Errorf("inbox/0.1.1 has %d entries", len(entries))
	}

	if err := p.Stage(state); code(err) != CodeBusy {
		t.Errorf("second stage: %v", err)
	}
	os.Remove(filepath.Join(state, "inbox", "request"))
	os.WriteFile(filepath.Join(Dir, "applying"), []byte("0.1.1\n"), 0o644)
	if err := p.Stage(state); code(err) != CodeBusy {
		t.Errorf("stage while applying: %v", err)
	}
	os.Remove(filepath.Join(Dir, "applying"))

	notDir := filepath.Join(state, "file")
	os.WriteFile(notDir, nil, 0o640)
	if err := p.Stage(notDir); code(err) != CodeIOError {
		t.Errorf("unwritable state: %v", err)
	}
}

func TestStageRemovesPartial(t *testing.T) {
	useDir(t)
	state := t.TempDir()
	// A directory in place of the request temp file makes Stage fail after the release
	// files were written.
	os.MkdirAll(filepath.Join(state, "inbox", ".request.tmp"), 0o750)
	p, err := Validate(bytes.NewReader(pkg(t, aarch64, nil)), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Stage(state); code(err) != CodeIOError {
		t.Fatalf("stage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "inbox", "0.1.1")); !os.IsNotExist(err) {
		t.Errorf("partial inbox/0.1.1 left behind: %v", err)
	}
}

func TestConfirm(t *testing.T) {
	state := t.TempDir()
	if err := Confirm(state, "0.1.2"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "confirmed")); string(b) != "0.1.2\n" {
		t.Errorf("confirmed = %q", b)
	}
	if err := Confirm(filepath.Join(state, "missing"), "0.1.2"); err == nil {
		t.Error("confirm into missing dir succeeded")
	}
}

func TestReadInfo(t *testing.T) {
	old := releaseRoot
	t.Cleanup(func() { releaseRoot = old })
	releaseRoot = t.TempDir()
	dir := useDir(t)
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "inbox"), 0o750)

	info := ReadInfo(state, "dev")
	if info != (UpdateInfo{State: "idle", CurrentVersion: "dev"}) {
		t.Errorf("empty: %+v", info)
	}
	js, _ := json.Marshal(info)
	if string(js) != `{"state":"idle","current_version":"dev","previous_version":null,"pending":false,"last_result":null}` {
		t.Errorf("json %s", js)
	}

	os.Symlink("releases/0.1.1", filepath.Join(releaseRoot, "current"))
	os.Symlink("releases/0.1.0", filepath.Join(releaseRoot, "previous"))
	os.WriteFile(filepath.Join(dir, "last-result"), []byte("installed 0.1.1\n"), 0o640)
	info = ReadInfo(state, "0.1.1")
	if info != (UpdateInfo{State: "idle", CurrentVersion: "0.1.1", PreviousVersion: "0.1.0", LastResult: "installed 0.1.1"}) {
		t.Errorf("released: %+v", info)
	}
	// Symlink already swapped by the updater, old binary still running: report the running one.
	if info = ReadInfo(state, "0.1.0"); info.CurrentVersion != "0.1.0" || info.PreviousVersion != "0.1.1" {
		t.Errorf("before restart: %+v", info)
	}

	os.WriteFile(filepath.Join(dir, "applying"), []byte("0.1.2\n"), 0o640)
	if info = ReadInfo(state, "0.1.1"); info.State != "applying" || info.Pending {
		t.Errorf("applying: %+v", info)
	}
	os.Remove(filepath.Join(dir, "applying"))

	os.WriteFile(filepath.Join(dir, "pending"), []byte("0.1.2 1\n"), 0o644)
	if info = ReadInfo(state, "dev"); info.State != "applying" || !info.Pending {
		t.Errorf("pending: %+v", info)
	}
	// Confirmed by the daemon: boot-guard has not removed pending yet.
	os.WriteFile(filepath.Join(state, "confirmed"), []byte("0.1.2\n"), 0o640)
	if info = ReadInfo(state, "0.1.2"); info.State != "idle" || info.Pending {
		t.Errorf("confirmed: %+v", info)
	}
	os.WriteFile(filepath.Join(state, "confirmed"), []byte("0.1.1\n"), 0o640)
	if info = ReadInfo(state, "0.1.1"); info.State != "applying" || !info.Pending {
		t.Errorf("confirmed other version: %+v", info)
	}
	os.WriteFile(filepath.Join(state, "inbox", "request"), []byte("0.1.2\n"), 0o640)
	if info = ReadInfo(state, "dev"); info.State != "staged" {
		t.Errorf("request: %+v", info)
	}
}
