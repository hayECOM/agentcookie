//go:build unix

package sinkpush

import (
	"os"

	"golang.org/x/sys/unix"
)

// isExecutableFile reports whether path exists, is a regular file, and is
// executable by the current process. This uses unix.Access with X_OK to
// perform an actual executability check based on the process's effective
// uid/gid, rather than just checking permission bits (which could accept
// a file with execute permission only for a different user/group).
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	// unix.Access with X_OK checks if the current process can execute
	// the file, respecting uid/gid/supplementary groups.
	return unix.Access(path, unix.X_OK) == nil
}
