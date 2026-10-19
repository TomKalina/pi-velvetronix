package netmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner runs argv (no shell) and returns its stdout and exit code. err is non-nil
// when the command failed; for a non-zero exit it carries stderr. code is -1 when the
// process did not run to completion (not found, killed by timeout).
type Runner func(ctx context.Context, argv []string) (stdout []byte, code int, err error)

func execRunner(ctx context.Context, argv []string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return stdout.Bytes(), 0, nil
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return stdout.Bytes(), exit.ExitCode(), errors.New(strings.TrimSpace(stderr.String()))
	default:
		return stdout.Bytes(), -1, err
	}
}

// CmdError is a failed nmcli call. Msg never contains secrets.
type CmdError struct {
	Code int
	Msg  string
}

func (e *CmdError) Error() string { return fmt.Sprintf("nmcli exit %d: %s", e.Code, e.Msg) }

// secretKeys are nmcli property names whose value must never be logged.
var secretKeys = map[string]bool{
	"wifi-sec.psk":                 true,
	"802-11-wireless-security.psk": true,
}

// redact returns argv with secret values replaced by "***", plus those values.
func redact(argv []string) (safe, secrets []string) {
	safe = make([]string, len(argv))
	for i, a := range argv {
		if i > 0 && secretKeys[argv[i-1]] {
			safe[i] = "***"
			if a != "" {
				secrets = append(secrets, a)
			}
			continue
		}
		safe[i] = a
	}
	return safe, secrets
}

// nm runs "nmcli -t -e yes <args>" with a 15 s timeout; "--wait N" calls
// get N seconds plus a margin.
func (m *Manager) nm(ctx context.Context, args ...string) ([]byte, error) {
	timeout := 15 * time.Second
	for i, a := range args {
		if i+1 == len(args) {
			break
		}
		switch {
		case a == "--wait":
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				timeout = time.Duration(n+10) * time.Second
			}
		case a == "--rescan" && args[i+1] == "yes":
			timeout = 30 * time.Second // waits for the scan to finish
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append([]string{"nmcli", "-t", "-e", "yes"}, args...)
	out, code, err := m.run(ctx, argv)
	safe, secrets := redact(argv)
	if err == nil && code == 0 {
		m.log.Debug("nmcli", "argv", strings.Join(safe, " "))
		return out, nil
	}
	msg := fmt.Sprint(err)
	for _, s := range secrets {
		msg = strings.ReplaceAll(msg, s, "***")
	}
	m.log.Warn("nmcli failed", "argv", strings.Join(safe, " "), "code", code, "err", msg)
	return out, &CmdError{Code: code, Msg: msg}
}

// splitTerse splits one line of "nmcli -t -e yes" tabular output into fields:
// unescaped ':' separates fields, "\:" and "\\" are a literal ':' and '\'.
func splitTerse(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(fields, cur.String())
}

// rows parses tabular terse output into one field slice per non-empty line.
func rows(out []byte) [][]string {
	var r [][]string
	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSuffix(line, "\r"); line != "" {
			r = append(r, splitTerse(line))
		}
	}
	return r
}

// keyValues parses multiline terse output ("connection show <id>", "device show"):
// one "FIELD:value" per line. nmcli does not escape values in multiline mode
// (src/nmcli/utils.c prints prefix + text verbatim), so split at the first ':' only.
func keyValues(out []byte) map[string]string {
	kv := map[string]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSuffix(line, "\r"), ":"); ok {
			kv[k] = v
		}
	}
	return kv
}

// parseAddedUUID extracts the UUID from "Connection 'x' (uuid) successfully added."
// (a message nmcli keeps stable for scripts under LC_ALL=C).
func parseAddedUUID(out []byte) string {
	s := string(out)
	end := strings.LastIndex(s, ") successfully added.")
	if end < 0 {
		return ""
	}
	start := strings.LastIndex(s[:end], "(")
	if start < 0 {
		return ""
	}
	return s[start+1 : end]
}
