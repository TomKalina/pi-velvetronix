package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func init() { bcryptCost = bcrypt.MinCost }

const pw = "correct horse"

func setup(t *testing.T) (string, *Store, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	if err := SetPassword(dir, pw); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return dir, s, &now
}

func TestSetPasswordLength(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"", "1234567", strings.Repeat("x", 73)} {
		if err := SetPassword(dir, p); !errors.Is(err, ErrBadLength) {
			t.Errorf("len %d: %v", len(p), err)
		}
	}
	for _, p := range []string{"12345678", strings.Repeat("x", 72)} {
		if err := SetPassword(dir, p); err != nil {
			t.Errorf("len %d: %v", len(p), err)
		}
	}
	var a authData
	data, _ := os.ReadFile(filepath.Join(dir, authFile))
	if err := json.Unmarshal(data, &a); err != nil || !strings.HasPrefix(a.Bcrypt, "$2") {
		t.Fatalf("auth.json = %s", data)
	}
	if _, err := time.Parse(time.RFC3339, a.Changed); err != nil {
		t.Errorf("changed: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, authFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestUnconfigured(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Configured() {
		t.Error("configured without auth.json")
	}
	if _, err := s.Login("whatever1", "1.2.3.4"); !errors.Is(err, ErrNoPassword) {
		t.Errorf("login: %v", err)
	}
}

func TestCorruptAuth(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, authFile), []byte("{"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Error("corrupt auth.json accepted")
	}
}

func TestLoginAndSessions(t *testing.T) {
	dir, s, now := setup(t)
	if !s.Configured() {
		t.Fatal("not configured")
	}
	if _, err := s.Login("wrong password", "10.0.0.1"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	tok, err := s.Login(pw, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 || strings.ContainsAny(tok, "+/=") {
		t.Errorf("token %q is not base64url of 32 bytes", tok)
	}
	if !s.Check(tok) || s.Check("nope") || s.Check("") {
		t.Fatal("check")
	}

	// Only the sha256 is stored.
	data, _ := os.ReadFile(filepath.Join(dir, sessionsFile))
	if strings.Contains(string(data), tok) || !strings.Contains(string(data), hashToken(tok)) {
		t.Fatalf("sessions.json = %s", data)
	}

	// Sessions survive a restart.
	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2.now = s.now
	if !s2.Check(tok) {
		t.Fatal("session lost after reload")
	}

	// Expiry after 30 days.
	*now = now.Add(30*24*time.Hour + time.Second)
	if s.Check(tok) {
		t.Error("expired session accepted")
	}

	tok2, _ := s.Login(pw, "10.0.0.1")
	s.Logout(tok2)
	if s.Check(tok2) {
		t.Error("logged out session accepted")
	}
	s3, _ := Load(dir)
	s3.now = s.now
	if s3.Check(tok2) {
		t.Error("logout not persisted")
	}
}

func TestMaxSessions(t *testing.T) {
	dir, s, now := setup(t)
	var toks []string
	for i := 0; i < maxSessions+3; i++ {
		*now = now.Add(time.Minute)
		tok, err := s.Login(pw, "10.0.0.2")
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
	}
	for i, tok := range toks {
		if want := i >= 3; s.Check(tok) != want {
			t.Errorf("session %d valid=%v, want %v", i, !want, want)
		}
	}
	var list []session
	data, _ := os.ReadFile(filepath.Join(dir, sessionsFile))
	json.Unmarshal(data, &list)
	if len(list) != maxSessions {
		t.Errorf("stored %d sessions", len(list))
	}
}

func TestRateLimit(t *testing.T) {
	_, s, now := setup(t)
	ip := "192.168.1.10"
	for i := 0; i < 4; i++ {
		if _, err := s.Login("bad password", ip); !errors.Is(err, ErrBadPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantBackoff := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
	for i, want := range wantBackoff {
		if _, err := s.Login("bad password", ip); !errors.Is(err, ErrBadPassword) {
			t.Fatalf("failure %d: %v", i+5, err)
		}
		_, err := s.Login(pw, ip)
		var rl ErrRateLimited
		if !errors.As(err, &rl) || rl.RetryAfter != want*time.Second {
			t.Fatalf("after failure %d: %v, want retry after %ds", i+5, err, want)
		}
		// Other IPs are not affected.
		if _, err := s.Login("bad password", "192.168.1.11"); !errors.Is(err, ErrBadPassword) {
			t.Fatalf("other ip: %v", err)
		}
		delete(s.fails, "192.168.1.11")
		*now = now.Add(want * time.Second)
	}

	// Failures older than 10 minutes are forgotten.
	*now = now.Add(11 * time.Minute)
	if _, err := s.Login("bad password", ip); !errors.Is(err, ErrBadPassword) {
		t.Fatal(err)
	}
	if _, err := s.Login(pw, ip); err != nil {
		t.Fatalf("login after window: %v", err)
	}
	if _, ok := s.fails[ip]; ok {
		t.Error("failures not cleared after success")
	}
}

// loginBurst starts n parallel wrong logins from ip while bcrypt is held, expects
// all but want of them to be refused at once, then releases bcrypt and expects want
// compares (bad_password).
func loginBurst(t *testing.T, s *Store, ip string, n, want int) {
	t.Helper()
	errs := make(chan error, n)
	s.compareMu.Lock()
	for range n {
		go func() {
			_, err := s.Login("bad password", ip)
			errs <- err
		}()
	}
	for i := 0; i < n-want; i++ {
		select {
		case err := <-errs:
			var rl ErrRateLimited
			if !errors.As(err, &rl) || rl.RetryAfter <= 0 {
				s.compareMu.Unlock()
				t.Fatalf("refused attempt %d: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			s.compareMu.Unlock()
			t.Fatalf("only %d of %d attempts refused while bcrypt was busy", i, n-want)
		}
	}
	s.compareMu.Unlock()
	for i := 0; i < want; i++ {
		if err := <-errs; !errors.Is(err, ErrBadPassword) {
			t.Fatalf("compared attempt %d: %v", i, err)
		}
	}
}

func TestRateLimitParallel(t *testing.T) {
	_, s, now := setup(t)
	ip := "192.168.1.20"
	loginBurst(t, s, ip, 30, freeFailures)
	if f := s.fails[ip]; len(f.times) != freeFailures || f.inflight != 0 {
		t.Fatalf("failures %d, inflight %d", len(f.times), f.inflight)
	}
	// After the backoff only one attempt at a time gets a compare.
	*now = now.Add(time.Second)
	loginBurst(t, s, ip, 10, 1)
	var rl ErrRateLimited
	if _, err := s.Login(pw, ip); !errors.As(err, &rl) || rl.RetryAfter != 2*time.Second {
		t.Fatalf("after burst: %v, want retry after 2s", err)
	}
}

func TestSetPasswordInvalidatesSessions(t *testing.T) {
	dir, s, _ := setup(t)
	tok, err := s.Login(pw, "10.0.0.3")
	if err != nil {
		t.Fatal(err)
	}
	// Running daemon, password changed by `set-password` in another process.
	if err := SetPassword(dir, "another password"); err != nil {
		t.Fatal(err)
	}
	if s.Check(tok) {
		t.Error("old session valid after password change")
	}
	if _, err := s.Login(pw, "10.0.0.3"); !errors.Is(err, ErrBadPassword) {
		t.Errorf("old password: %v", err)
	}
	if _, err := s.Login("another password", "10.0.0.3"); err != nil {
		t.Errorf("new password: %v", err)
	}
	s2, _ := Load(dir)
	if s2.Check(tok) {
		t.Error("old session persisted")
	}
}
