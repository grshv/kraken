package runtime

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// RemoveStalePID deletes a Squid PID file unless it names a running squid
// process. Absent file is a no-op; unparseable or non-positive content, a dead
// PID (comm error) or a non-squid comm all cause removal. A delete failure is
// returned so the caller (squid-start) surfaces it and lets Pebble retry.
func RemoveStalePID(path string, comm func(pid int) (string, error)) (bool, error) {
	data, e := os.ReadFile(path)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return false, nil
		}
		return false, e
	}
	if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
		if name, ce := comm(pid); ce == nil && name == "squid" {
			return false, nil
		}
	}
	if e := os.Remove(path); e != nil {
		return false, e
	}
	return true, nil
}

// ProcComm returns /proc/<pid>/comm without the trailing newline.
func ProcComm(pid int) (string, error) {
	b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if e != nil {
		return "", e
	}
	return strings.TrimRight(string(b), "\n"), nil
}
