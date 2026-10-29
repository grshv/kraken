//go:build linux

package runtime

import (
	"fmt"
	"syscall"
)

// tmpfsMagic is the Linux TMPFS_MAGIC (0x01021994). The run directory must
// live on tmpfs so derivative files never survive a container restart: the
// manager secret, squid.conf and proxy-accounts export are regenerated from
// the control database on every start.
const tmpfsMagic = 0x01021994

// verifyTmpfs rejects a parent that is not a tmpfs mount. On Linux the run
// directory is /dev/shm/kraken by default; refusing anything else prevents an
// operator from persisting secrets and derivative state to a regular volume.
func verifyTmpfs(dir string) error {
	var s syscall.Statfs_t
	if e := syscall.Statfs(dir, &s); e != nil {
		return fmt.Errorf("cannot stat run directory parent %s: %w", dir, e)
	}
	if s.Type != tmpfsMagic {
		return fmt.Errorf("%s is not on tmpfs; set KRAKEN_RUN_DIR to a tmpfs path", dir)
	}
	return nil
}
