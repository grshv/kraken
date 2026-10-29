package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// proxyGroupID is the GID of the proxy group that Squid runs as. serve chgrps
// the run directory to it so Squid can traverse and read the shared derivative
// files (squid.conf, manager-auth.conf, proxy-accounts.json). Tests override it
// to the runner's GID since they do not run under the proxy group.
var proxyGroupID = 13

// runDirMode is the mode every run directory must have: group proxy can traverse
// and read but never create, replace or delete entries (a compromised Squid must
// not swap the config, manager secret or accounts export), others have no
// access, and the setgid bit makes files created inside inherit the proxy group. os.ModeSetgid
// is a high FileMode bit (1<<22) that syscallMode translates to the Unix
// S_ISGID (0o2000); a bare 0o2770 literal would silently drop the setgid bit
// because 0o2000 is neither in ModePerm (bits 0–8) nor in ModeSetgid.
const runDirMode = os.ModeSetgid | 0o750

// PrepareRunDir validates and creates the tmpfs run directory. The directory
// must live on tmpfs (derivative files never persist) and, when it already
// exists, must be owned by the current user with no access for others and no
// foreign files — a pre-existing directory planted by another user could hold
// spoofed config or a hostile manager-auth.conf. The setgid bit makes files
// created inside inherit the proxy group so Squid can read them.
func PrepareRunDir(dir string) error {
	if e := verifyTmpfs(filepath.Dir(dir)); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(dir), 0755); e != nil {
		return e
	}
	info, e := os.Stat(dir)
	if e == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", dir)
		}
		if e := checkRunDirOwnership(dir, info); e != nil {
			return e
		}
	} else if errors.Is(e, os.ErrNotExist) {
		if e := os.Mkdir(dir, runDirMode); e != nil {
			return e
		}
		if info, e = os.Stat(dir); e != nil {
			return e
		}
	} else {
		return e
	}
	// The current user is a member of the proxy group; on Linux a non-root
	// owner may chgrp a directory it owns to a group it belongs to.
	if e := os.Chown(dir, -1, proxyGroupID); e != nil {
		return fmt.Errorf("cannot set run directory group to proxy: %w", e)
	}
	if info.Mode()&runDirMode != runDirMode {
		if e := os.Chmod(dir, runDirMode); e != nil {
			return e
		}
	}
	return nil
}

// ChownProxyGroup chowns path to the proxy group without changing the owner.
// Used after atomic writes of derivative files (squid.conf, manager-auth.conf,
// proxy-accounts.json) so Squid — which runs as the proxy user — can read them
// even when the setgid bit on the directory is insufficient (e.g. the file was
// moved into place by rename rather than created in-place). apply.lock is 0600
// and owner-only; it is never chowned.
func ChownProxyGroup(path string) error {
	return os.Chown(path, -1, proxyGroupID)
}

// checkRunDirOwnership enforces the anti-spoofing invariant: the directory and
// every entry in it must be owned by the current user, and others must have no
// access. A directory that fails any clause is rejected rather than reused.
func checkRunDirOwnership(dir string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot inspect run directory ownership")
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not owned by the current user", dir)
	}
	if info.Mode().Perm()&0007 != 0 {
		return fmt.Errorf("%s is accessible by others", dir)
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, en := range entries {
		fi, e := en.Info()
		if e != nil {
			return e
		}
		s, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || int(s.Uid) != os.Getuid() {
			return fmt.Errorf("%s contains a foreign file: %s", dir, en.Name())
		}
	}
	return nil
}

// PrepareSquidRunDir creates Squid's private tmpfs directory for its PID file.
// squid-start calls it as the proxy user, so the directory belongs to Squid
// alone (0700) and the panel run directory stays read-only for Squid. An
// existing directory must be owned by the current user with no group or other
// access; a stale PID file left by a killed Squid in the same container is
// removed unless it still names a live squid process.
func PrepareSquidRunDir(dir string) error {
	if e := verifyTmpfs(filepath.Dir(dir)); e != nil {
		return e
	}
	info, e := os.Stat(dir)
	if errors.Is(e, os.ErrNotExist) {
		if e = os.Mkdir(dir, 0o700); e != nil {
			return e
		}
		if info, e = os.Stat(dir); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", dir)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not owned by the current user", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is accessible by group or others", dir)
	}
	_, e = RemoveStalePID(filepath.Join(dir, "squid.pid"), ProcComm)
	return e
}
