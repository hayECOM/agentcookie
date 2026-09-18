//go:build !unix

package sinkpush

import "os"

// isExecutableFile reports whether path exists and is a regular file.
// On non-Unix systems (e.g., Windows), we cannot use unix.Access to check
// execute permission. We fall back to checking if the file exists and is
// regular. exec.Command will fail at runtime if the file is not executable.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
