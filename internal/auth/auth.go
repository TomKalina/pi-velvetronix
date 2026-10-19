// Package auth stores the web password (bcrypt), login sessions and a per-IP login backoff.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrBadPassword = errors.New("bad_password")
	ErrNoPassword  = errors.New("no_password")
	ErrBadLength   = errors.New("password must be 8..72 bytes")
)

// ErrRateLimited is returned by Login while the client IP is backing off.
type ErrRateLimited struct{ RetryAfter time.Duration }

func (e ErrRateLimited) Error() string {
	return fmt.Sprintf("rate_limited (retry after %s)", e.RetryAfter)
}

const (
	authFile     = "auth.json"
	sessionsFile = "sessions.json"

	sessionTTL     = 30 * 24 * time.Hour
	maxSessions    = 16
	failWindow     = 10 * time.Minute
	freeFailures   = 5
	maxBackoff     = 60 * time.Second
	minPasswordLen = 8
	maxPasswordLen = 72
)

// bcryptCost is a variable only so tests can lower it.
var bcryptCost = 11

type authData struct {
	Bcrypt  string `json:"bcrypt"`
	Changed string `json:"changed"`
}

type session struct {
	Hash string    `json:"h"`
	Exp  time.Time `json:"exp"`
}

type failures struct {
	times    []time.Time
	until    time.Time
	inflight int // attempts between the backoff check and their recorded result
}

// Store holds the password hash and sessions of one state directory.
type Store struct {
	dir string
	now func() time.Time

	compareMu sync.Mutex // one bcrypt compare at a time

	mu       sync.Mutex
	hash     []byte
	authStat os.FileInfo
	sessions []session
	fails    map[string]*failures
}

func statAuth(dir string) os.FileInfo {
	fi, _ := os.Stat(filepath.Join(dir, authFile))
	return fi
}

// sameFile reports whether a and b describe the same, unmodified file (or both are absent).
func sameFile(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.ModTime().Equal(b.ModTime()) && a.Size() == b.Size()
}

// Load reads auth.json and sessions.json. A missing auth.json is not an error: the store
// is then unconfigured and every login fails with ErrNoPassword.
func Load(stateDir string) (*Store, error) {
	s := &Store{dir: stateDir, now: time.Now, fails: map[string]*failures{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	// Corrupt or missing sessions only log everybody out.
	if data, err := os.ReadFile(filepath.Join(stateDir, sessionsFile)); err == nil && s.hash != nil {
		_ = json.Unmarshal(data, &s.sessions)
	}
	return s, nil
}

// load reads the password hash. Caller holds s.mu (or owns s exclusively).
func (s *Store) load() error {
	s.hash = nil
	s.authStat = statAuth(s.dir)
	data, err := os.ReadFile(filepath.Join(s.dir, authFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var a authData
	if err := json.Unmarshal(data, &a); err != nil || a.Bcrypt == "" {
		return fmt.Errorf("parse %s: invalid content", authFile)
	}
	s.hash = []byte(a.Bcrypt)
	return nil
}

// reloadIfChanged picks up a password set by `set-password` while the daemon runs; a
// changed password invalidates every session. Caller holds s.mu.
func (s *Store) reloadIfChanged() {
	if sameFile(statAuth(s.dir), s.authStat) {
		return
	}
	_ = s.load() // on error hash stays nil: fail closed
	s.sessions = nil
	_ = s.saveSessions()
}

// Configured reports whether a password is set.
func (s *Store) Configured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChanged()
	return s.hash != nil
}

// Login checks the password and returns a new session token.
func (s *Store) Login(password, remoteIP string) (string, error) {
	s.mu.Lock()
	s.reloadIfChanged()
	hash := s.hash
	now := s.now()
	s.pruneFailures(now)
	f := s.fails[remoteIP]
	if f != nil && now.Before(f.until) {
		s.mu.Unlock()
		return "", ErrRateLimited{RetryAfter: f.until.Sub(now)}
	}
	if hash == nil {
		s.mu.Unlock()
		return "", ErrNoPassword
	}
	// Reserve the attempt so parallel requests see it: the free failures can run in
	// parallel, after them only one attempt at a time.
	if f == nil {
		f = &failures{}
		s.fails[remoteIP] = f
	}
	if f.inflight > 0 && f.inflight+len(f.times) >= freeFailures {
		s.mu.Unlock()
		return "", ErrRateLimited{RetryAfter: time.Second}
	}
	f.inflight++
	s.mu.Unlock()

	s.compareMu.Lock()
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	s.compareMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	now = s.now()
	f.inflight--
	s.pruneFailures(now)
	if err != nil {
		f := s.fails[remoteIP]
		if f == nil {
			f = &failures{}
			s.fails[remoteIP] = f
		}
		f.times = append(f.times, now)
		if n := len(f.times); n >= freeFailures {
			d := time.Second << min(n-freeFailures, 6)
			f.until = now.Add(min(d, maxBackoff))
		}
		return "", ErrBadPassword
	}
	if f.inflight > 0 {
		f.times, f.until = nil, time.Time{} // keep the reservations of parallel attempts
	} else {
		delete(s.fails, remoteIP)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.pruneSessions(now)
	s.sessions = append(s.sessions, session{Hash: hashToken(token), Exp: now.Add(sessionTTL)})
	if len(s.sessions) > maxSessions {
		sort.Slice(s.sessions, func(i, j int) bool { return s.sessions[i].Exp.Before(s.sessions[j].Exp) })
		s.sessions = s.sessions[len(s.sessions)-maxSessions:]
	}
	if err := s.saveSessions(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) pruneFailures(now time.Time) {
	for ip, f := range s.fails {
		kept := f.times[:0]
		for _, t := range f.times {
			if now.Sub(t) < failWindow {
				kept = append(kept, t)
			}
		}
		f.times = kept
		if len(kept) == 0 && f.inflight == 0 && !now.Before(f.until) {
			delete(s.fails, ip)
		}
	}
}

func (s *Store) pruneSessions(now time.Time) {
	kept := s.sessions[:0]
	for _, se := range s.sessions {
		if now.Before(se.Exp) {
			kept = append(kept, se)
		}
	}
	s.sessions = kept
}

// Check reports whether token belongs to a valid session.
func (s *Store) Check(token string) bool {
	if token == "" {
		return false
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChanged()
	now := s.now()
	for _, se := range s.sessions {
		if se.Hash == h && now.Before(se.Exp) {
			return true
		}
	}
	return false
}

// Logout removes the session of token.
func (s *Store) Logout(token string) {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, se := range s.sessions {
		if se.Hash == h {
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			_ = s.saveSessions()
			return
		}
	}
}

func (s *Store) saveSessions() error {
	list := s.sessions
	if list == nil {
		list = []session{}
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, sessionsFile), data, 0o600)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SetPassword writes a new password hash and invalidates all sessions.
func SetPassword(stateDir, password string) error {
	if len(password) < minPasswordLen || len(password) > maxPasswordLen {
		return ErrBadLength
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(stateDir, sessionsFile), []byte("[]"), 0o600); err != nil {
		return err
	}
	data, err := json.Marshal(authData{Bcrypt: string(hash), Changed: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(stateDir, authFile), data, 0o600)
}

// writeAtomic writes via a temp file in the same directory: fsync, rename, fsync dir.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
