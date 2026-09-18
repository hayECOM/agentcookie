package sinkpush

import (
	"os"
	"os/exec"
	"path/filepath"
)

// findPPCLI locates a printing-press CLI binary by name and optional aliases.
// It searches well-known installation directories in order of preference,
// ensuring LaunchAgents and stripped-PATH daemons can still find binaries
// that exist on disk. The search order is:
//
//  1. ~/.local/bin/<name> (printing-press installer path)
//  2. ~/go/bin/<name> (canonical go install location)
//  3. ~/bin/<name> (user bin directory)
//  4. exec.LookPath(name) (PATH lookup)
//  5. For each alias: exec.LookPath(alias)
//
// Returns an empty string if the binary is not found at any location.
// Callers should check IsInstalled() which stats the returned path.
func findPPCLI(name string, aliases ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to PATH lookups only if home dir is unavailable.
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		for _, alias := range aliases {
			if p, err := exec.LookPath(alias); err == nil {
				return p
			}
		}
		return ""
	}

	// Well-known absolute paths to check, in order of preference.
	// ~/.local/bin is the printing-press installer path (preferred).
	// ~/go/bin is the canonical go install location.
	// ~/bin is a common user bin directory.
	wellKnownDirs := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, "bin"),
	}

	// Check well-known directories first (handles stripped PATH).
	// Require executable permission to avoid a non-executable junk file
	// shadowing a valid executable in a later directory.
	for _, dir := range wellKnownDirs {
		path := filepath.Join(dir, name)
		if isExecutableFile(path) {
			return path
		}
	}

	// Fall back to PATH lookup for the primary name.
	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	// Check aliases on PATH (e.g., "instacart" as an alias for "instacart-pp-cli").
	for _, alias := range aliases {
		if p, err := exec.LookPath(alias); err == nil {
			return p
		}
	}

	// Not found anywhere. Return the preferred path (first well-known dir)
	// so error messages reference a sensible location. This matches the
	// previous behavior of findInstacartBinary.
	return filepath.Join(wellKnownDirs[0], name)
}
