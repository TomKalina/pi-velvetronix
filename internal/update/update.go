// Package update validates and stages update packages and reports update status.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Error codes (design §5).
const (
	CodeTooLarge        = "too_large"
	CodeNotGzip         = "not_gzip"
	CodeBadTar          = "bad_tar"
	CodeUnexpectedEntry = "unexpected_entry"
	CodeMissingEntry    = "missing_entry"
	CodeBadManifest     = "bad_manifest"
	CodeWrongName       = "wrong_name"
	CodeWrongArch       = "wrong_arch"
	CodeBadVersion      = "bad_version"
	CodeSameVersion     = "same_version"
	CodeSHA256Mismatch  = "sha256_mismatch"
	CodeNotELF          = "not_elf"
	CodeNotAArch64      = "not_aarch64"
	CodeBusy            = "busy"
	CodeIOError         = "io_error"
)

// Error is a validation or staging failure with a stable code.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func fail(code, detail string) *Error { return &Error{Code: code, Detail: detail} }

const (
	manifestName = "manifest.json"
	binaryName   = "pi-velvetronix"
	maxManifest  = 4 << 10
)

// MaxBinary is the size limit of the binary and of the uploaded package (40 MiB).
const MaxBinary = 40 << 20

// Limits are variables only so tests can use small values.
var (
	maxBinary  int64 = MaxBinary
	maxPackage int64 = MaxBinary
)

var versionRE = regexp.MustCompile(`^[0-9A-Za-z._-]{1,64}$`)

// Manifest is manifest.json of an update package.
type Manifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	Binary  string `json:"binary"`
	SHA256  string `json:"sha256"`
	Built   string `json:"built"`
}

// Package is a validated update package held in memory.
type Package struct {
	Manifest Manifest
	manifest []byte
	binary   []byte
}

// countingReader fails once more than limit bytes were read.
type countingReader struct {
	r     io.Reader
	n     int64
	limit int64
}

var errTooLarge = errors.New("package too large")

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.limit {
		return n, errTooLarge
	}
	return n, err
}

// Validate reads a .tar.gz update package and checks it in the order of design §6.
// running is the version of the running daemon. Errors are *Error.
func Validate(r io.Reader, running string) (*Package, error) {
	cr := &countingReader{r: r, limit: maxPackage}
	br := bufio.NewReader(cr)
	magic, err := br.Peek(6)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, readErr(err, CodeNotGzip)
	}
	if len(magic) < 2 || magic[0] != 0x1F || magic[1] != 0x8B {
		return nil, fail(CodeNotGzip, describe(magic))
	}
	gz, err := gzip.NewReader(br)
	if err != nil {
		return nil, readErr(err, CodeNotGzip)
	}

	p := &Package{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, tar.ErrInsecurePath) {
			return nil, fail(CodeUnexpectedEntry, hdr.Name)
		}
		if err != nil {
			return nil, readErr(err, CodeBadTar)
		}
		var dst *[]byte
		var limit int64
		switch {
		case hdr.Typeflag != tar.TypeReg:
			return nil, fail(CodeUnexpectedEntry, hdr.Name)
		case hdr.Name == manifestName:
			dst, limit = &p.manifest, maxManifest
		case hdr.Name == binaryName:
			dst, limit = &p.binary, maxBinary
		default:
			return nil, fail(CodeUnexpectedEntry, hdr.Name)
		}
		if *dst != nil {
			return nil, fail(CodeUnexpectedEntry, "duplicate "+hdr.Name)
		}
		if hdr.Size > limit {
			return nil, fail(CodeTooLarge, hdr.Name)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, readErr(err, CodeBadTar)
		}
		*dst = data
	}
	// Read to the end so the gzip checksum is verified.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return nil, readErr(err, CodeBadTar)
	}
	if p.manifest == nil {
		return nil, fail(CodeMissingEntry, manifestName)
	}
	if p.binary == nil {
		return nil, fail(CodeMissingEntry, binaryName)
	}

	m := &p.Manifest
	if err := json.Unmarshal(p.manifest, m); err != nil {
		return nil, fail(CodeBadManifest, "invalid json")
	}
	want, err := hex.DecodeString(m.SHA256)
	if err != nil || len(want) != sha256.Size || m.Binary != binaryName {
		return nil, fail(CodeBadManifest, "sha256 or binary field")
	}
	if m.Name != "pi-velvetronix" {
		return nil, fail(CodeWrongName, m.Name)
	}
	if m.Arch != "linux/arm64" {
		return nil, fail(CodeWrongArch, m.Arch)
	}
	// Dot names (".", "..", ".request.tmp") would escape inbox/<ver> or hide from the
	// path unit; "request" and "rollback" collide with the inbox request protocol.
	if !versionRE.MatchString(m.Version) || strings.HasPrefix(m.Version, ".") ||
		m.Version == "request" || m.Version == "rollback" {
		return nil, fail(CodeBadVersion, "")
	}
	if m.Version == running {
		return nil, fail(CodeSameVersion, m.Version)
	}
	if sum := sha256.Sum256(p.binary); !bytes.Equal(sum[:], want) {
		return nil, fail(CodeSHA256Mismatch, "")
	}
	if err := checkELF(p.binary); err != nil {
		return nil, err
	}
	return p, nil
}

func readErr(err error, code string) error {
	if errors.Is(err, errTooLarge) {
		return fail(CodeTooLarge, "")
	}
	return fail(code, "")
}

// describe names well-known non-gzip uploads for a more helpful error detail.
func describe(magic []byte) string {
	switch {
	case bytes.HasPrefix(magic, []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}):
		return "xz file (OS image?)"
	case bytes.HasPrefix(magic, []byte("PK\x03\x04")):
		return "zip file"
	case bytes.HasPrefix(magic, []byte("\x7FELF")):
		return "bare binary"
	}
	return ""
}

func checkELF(bin []byte) error {
	if !bytes.HasPrefix(bin, []byte("\x7FELF")) {
		return fail(CodeNotELF, "")
	}
	f, err := elf.NewFile(bytes.NewReader(bin))
	if err != nil {
		return fail(CodeNotELF, "")
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != elf.EM_AARCH64 ||
		(f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return fail(CodeNotAArch64, f.Machine.String())
	}
	return nil
}

// Stage writes inbox/<ver>/{manifest.json,pi-velvetronix} and then inbox/request.
// On an error before the request is published it removes the partial inbox/<ver>.
func (p *Package) Stage(stateDir string) error {
	inbox := filepath.Join(stateDir, "inbox")
	request := filepath.Join(inbox, "request")
	if exists(request) || exists(filepath.Join(Dir, "applying")) {
		return fail(CodeBusy, "")
	}
	dir := filepath.Join(inbox, p.Manifest.Version)
	if err := p.stage(inbox, request, dir); err != nil {
		if !exists(request) {
			os.RemoveAll(dir)
		}
		return err
	}
	return nil
}

func (p *Package) stage(inbox, request, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return ioErr(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ioErr(err)
	}
	if err := writeAtomic(filepath.Join(dir, manifestName), p.manifest, 0o640); err != nil {
		return ioErr(err)
	}
	if err := writeAtomic(filepath.Join(dir, binaryName), p.binary, 0o640); err != nil {
		return ioErr(err)
	}
	if err := syncDir(inbox); err != nil {
		return ioErr(err)
	}
	// The path unit ignores dot files, so the request appears only on rename.
	tmp := filepath.Join(inbox, ".request.tmp")
	if err := writeFileSync(tmp, []byte(p.Manifest.Version+"\n"), 0o640); err != nil {
		return ioErr(err)
	}
	if err := os.Rename(tmp, request); err != nil {
		return ioErr(err)
	}
	return ioErr(syncDir(inbox))
}

func ioErr(err error) error {
	if err == nil {
		return nil
	}
	return fail(CodeIOError, err.Error())
}

// UpdateInfo is the update part of the HTTP status. Empty strings encode as JSON null.
type UpdateInfo struct {
	State           string // "idle" | "staged" | "applying"
	CurrentVersion  string
	PreviousVersion string
	Pending         bool
	LastResult      string
}

func (u UpdateInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		State           string  `json:"state"`
		CurrentVersion  string  `json:"current_version"`
		PreviousVersion *string `json:"previous_version"`
		Pending         bool    `json:"pending"`
		LastResult      *string `json:"last_result"`
	}{u.State, u.CurrentVersion, null(u.PreviousVersion), u.Pending, null(u.LastResult)})
}

func null(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// releaseRoot is a variable only so tests can use a temp dir.
var releaseRoot = "/opt/pi-velvetronix"

// Dir is the root-owned update directory (pending, applying, last-result). The daemon
// only reads it; main sets it from --update-dir.
var Dir = "/var/lib/pi-velvetronix-update"

// Confirm marks version as started successfully by writing <state>/confirmed. boot-guard
// then drops the pending entry of that version instead of counting another attempt.
func Confirm(stateDir, version string) error {
	return writeAtomic(filepath.Join(stateDir, "confirmed"), []byte(version+"\n"), 0o640)
}

// ReadInfo derives the update status from the state directory, the update directory
// and the release symlinks: inbox/request present = staged; applying (updater running)
// or pending (new release not yet confirmed) present = applying. pending counts only
// while its version differs from <state>/confirmed. CurrentVersion is the version of
// the running binary: the current symlink already points to the new release before
// the restart, so it must not be reported as running.
func ReadInfo(stateDir, running string) UpdateInfo {
	info := UpdateInfo{State: "idle", CurrentVersion: running}
	info.PreviousVersion = linkVersion("previous")
	if info.PreviousVersion == running {
		// After a rollback previous points at the release that failed.
		info.PreviousVersion = linkVersion("current")
		if info.PreviousVersion == running {
			info.PreviousVersion = ""
		}
	}
	if pending, ok := firstLine(filepath.Join(Dir, "pending")); ok {
		ver, _, _ := strings.Cut(pending, " ")
		confirmed, ok := firstLine(filepath.Join(stateDir, "confirmed"))
		info.Pending = !ok || ver != confirmed
	}
	switch {
	case exists(filepath.Join(stateDir, "inbox", "request")):
		info.State = "staged"
	case info.Pending || exists(filepath.Join(Dir, "applying")):
		info.State = "applying"
	}
	info.LastResult, _ = firstLine(filepath.Join(Dir, "last-result"))
	return info
}

// firstLine returns the trimmed first line of at most 256 bytes of path.
func firstLine(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 256))
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(line), true
}

func linkVersion(name string) string {
	target, err := os.Readlink(filepath.Join(releaseRoot, name))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeAtomic writes via a temp file in the same directory: fsync, rename, fsync dir.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := writeFileSync(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
