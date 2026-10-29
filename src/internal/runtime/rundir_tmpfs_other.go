//go:build !linux

package runtime

// verifyTmpfs is a no-op on non-Linux hosts (development/test only). The
// container runtime always provides /dev/shm as tmpfs; the check exists to
// catch misconfiguration on real Linux deployments.
func verifyTmpfs(dir string) error { return nil }
