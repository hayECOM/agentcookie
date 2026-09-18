package sinkpush

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindPPCLI_LocalBinPreferred(t *testing.T) {
	// Create temp home with .local/bin and go/bin, both containing the binary.
	// .local/bin should win because it's checked first.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "") // Stripped PATH

	localBin := filepath.Join(tmpHome, ".local", "bin")
	goBin := filepath.Join(tmpHome, "go", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goBin, 0o755); err != nil {
		t.Fatal(err)
	}

	localPath := filepath.Join(localBin, "instacart-pp-cli")
	goPath := filepath.Join(goBin, "instacart-pp-cli")
	if err := os.WriteFile(localPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("instacart-pp-cli")
	if got != localPath {
		t.Errorf("findPPCLI with both .local/bin and go/bin: got %q, want %q", got, localPath)
	}
}

func TestFindPPCLI_OnlyLocalBin_StrippedPATH(t *testing.T) {
	// Binary ONLY at .local/bin/instacart-pp-cli, PATH without that dir.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "/nonexistent")

	localBin := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localBin, "instacart-pp-cli")
	if err := os.WriteFile(localPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("instacart-pp-cli")
	if got != localPath {
		t.Errorf("findPPCLI with only .local/bin (stripped PATH): got %q, want %q", got, localPath)
	}
}

func TestFindPPCLI_OnlyGoBin_StillWorks(t *testing.T) {
	// Only ~/go/bin -> still works.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	goBin := filepath.Join(tmpHome, "go", "bin")
	if err := os.MkdirAll(goBin, 0o755); err != nil {
		t.Fatal(err)
	}
	goPath := filepath.Join(goBin, "instacart-pp-cli")
	if err := os.WriteFile(goPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("instacart-pp-cli")
	if got != goPath {
		t.Errorf("findPPCLI with only go/bin: got %q, want %q", got, goPath)
	}
}

func TestFindPPCLI_AliasOnPATH(t *testing.T) {
	// Alias instacart on PATH when instacart-pp-cli missing -> Instacart installed.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create a temp bin dir with the alias and add to PATH.
	tmpBin := t.TempDir()
	aliasPath := filepath.Join(tmpBin, "instacart")
	if err := os.WriteFile(aliasPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpBin)

	got := findPPCLI("instacart-pp-cli", "instacart")
	if got != aliasPath {
		t.Errorf("findPPCLI with alias on PATH: got %q, want %q", got, aliasPath)
	}
}

func TestFindPPCLI_NothingAnywhere_ReturnsDefault(t *testing.T) {
	// Nothing anywhere -> returns default path (so error messages reference it).
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	got := findPPCLI("instacart-pp-cli", "instacart")
	// Should return the preferred path for error messages.
	expected := filepath.Join(tmpHome, ".local", "bin", "instacart-pp-cli")
	if got != expected {
		t.Errorf("findPPCLI with nothing installed: got %q, want %q", got, expected)
	}
}

func TestFindPPCLI_UserBin(t *testing.T) {
	// Only ~/bin -> found.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	userBin := filepath.Join(tmpHome, "bin")
	if err := os.MkdirAll(userBin, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(userBin, "airbnb-pp-cli")
	if err := os.WriteFile(binPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("airbnb-pp-cli")
	if got != binPath {
		t.Errorf("findPPCLI with ~/bin: got %q, want %q", got, binPath)
	}
}

func TestFindPPCLI_DirectoryNotFile_Skipped(t *testing.T) {
	// A directory at the expected path should not be considered a valid binary.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	localBin := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a directory instead of a file.
	dirPath := filepath.Join(localBin, "instacart-pp-cli")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// But put a real file in go/bin.
	goBin := filepath.Join(tmpHome, "go", "bin")
	if err := os.MkdirAll(goBin, 0o755); err != nil {
		t.Fatal(err)
	}
	goPath := filepath.Join(goBin, "instacart-pp-cli")
	if err := os.WriteFile(goPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("instacart-pp-cli")
	if got != goPath {
		t.Errorf("findPPCLI should skip directory and find go/bin: got %q, want %q", got, goPath)
	}
}

func TestFindPPCLI_PycookiecheatAdapters_LocalBin(t *testing.T) {
	// Test that pycookiecheat-style adapters (airbnb, ebay, pagliacci) can
	// also find their CLI at .local/bin.
	for _, name := range []string{"airbnb-pp-cli", "ebay-pp-cli", "pagliacci-pp-cli"} {
		t.Run(name, func(t *testing.T) {
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)
			t.Setenv("PATH", "")

			localBin := filepath.Join(tmpHome, ".local", "bin")
			if err := os.MkdirAll(localBin, 0o755); err != nil {
				t.Fatal(err)
			}
			localPath := filepath.Join(localBin, name)
			if err := os.WriteFile(localPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}

			got := findPPCLI(name)
			if got != localPath {
				t.Errorf("findPPCLI(%q) with .local/bin: got %q, want %q", name, got, localPath)
			}
		})
	}
}

func TestFindPPCLI_TableReservation_LocalBin(t *testing.T) {
	// Test table-reservation-goat-pp-cli at .local/bin.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	localBin := filepath.Join(tmpHome, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localBin, "table-reservation-goat-pp-cli")
	if err := os.WriteFile(localPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("table-reservation-goat-pp-cli")
	if got != localPath {
		t.Errorf("findPPCLI(table-reservation) with .local/bin: got %q, want %q", got, localPath)
	}
}

func TestFindPPCLI_NonExecutableFileShadowsNothing(t *testing.T) {
	// A non-executable file at ~/.local/bin should NOT shadow a valid
	// executable at ~/go/bin. This is P1 fix #3: unusable file shadows valid CLI.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	localBin := filepath.Join(tmpHome, ".local", "bin")
	goBin := filepath.Join(tmpHome, "go", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goBin, 0o755); err != nil {
		t.Fatal(err)
	}

	// Put a non-executable file at .local/bin (mode 0644, no execute bits).
	localPath := filepath.Join(localBin, "instacart-pp-cli")
	if err := os.WriteFile(localPath, []byte("junk file, not executable"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Put a valid executable at go/bin.
	goPath := filepath.Join(goBin, "instacart-pp-cli")
	if err := os.WriteFile(goPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("instacart-pp-cli")
	if got != goPath {
		t.Errorf("non-executable at .local/bin should not shadow go/bin: got %q, want %q", got, goPath)
	}
}

func TestFindPPCLI_OtherExecuteOnlyShadowsNothing(t *testing.T) {
	// A file with only "other" execute permission (mode 0001) at the preferred
	// location should NOT shadow a valid user-executable file at a later location.
	// The test process owns the file, so it cannot execute a file that only has
	// other-execute permission. This tests that unix.Access is correctly used
	// to check actual executability, not just mode bits.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	localBin := filepath.Join(tmpHome, ".local", "bin")
	goBin := filepath.Join(tmpHome, "go", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goBin, 0o755); err != nil {
		t.Fatal(err)
	}

	// Put a file with only other-execute permission at .local/bin (mode 0001).
	// The test process is the owner, so it cannot execute this file.
	localPath := filepath.Join(localBin, "instacart-pp-cli")
	if err := os.WriteFile(localPath, []byte("#!/bin/bash\nexit 0\n"), 0o001); err != nil {
		t.Fatal(err)
	}

	// Put a valid user-executable at go/bin.
	goPath := filepath.Join(goBin, "instacart-pp-cli")
	if err := os.WriteFile(goPath, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("instacart-pp-cli")
	if got != goPath {
		t.Errorf("other-execute-only at .local/bin should not shadow go/bin: got %q, want %q", got, goPath)
	}
}

func TestFindPPCLI_NonExecutableEverywhere_ReturnsDefault(t *testing.T) {
	// If only non-executable files exist, should return default path for error messages.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "")

	localBin := filepath.Join(tmpHome, ".local", "bin")
	goBin := filepath.Join(tmpHome, "go", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goBin, 0o755); err != nil {
		t.Fatal(err)
	}

	// Put non-executable files at both locations.
	if err := os.WriteFile(filepath.Join(localBin, "instacart-pp-cli"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goBin, "instacart-pp-cli"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := findPPCLI("instacart-pp-cli")
	// Should return default path for error messages since nothing is executable.
	expected := filepath.Join(tmpHome, ".local", "bin", "instacart-pp-cli")
	if got != expected {
		t.Errorf("no executable anywhere should return default: got %q, want %q", got, expected)
	}
}

func TestIsExecutableFile(t *testing.T) {
	dir := t.TempDir()

	// Test executable file (mode 0755).
	execPath := filepath.Join(dir, "exec")
	if err := os.WriteFile(execPath, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFile(execPath) {
		t.Errorf("isExecutableFile(%q) = false, want true for mode 0755", execPath)
	}

	// Test non-executable file (mode 0644).
	nonExecPath := filepath.Join(dir, "nonexec")
	if err := os.WriteFile(nonExecPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isExecutableFile(nonExecPath) {
		t.Errorf("isExecutableFile(%q) = true, want false for mode 0644", nonExecPath)
	}

	// Test directory (even with execute bit, should be false).
	dirPath := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if isExecutableFile(dirPath) {
		t.Errorf("isExecutableFile(%q) = true, want false for directory", dirPath)
	}

	// Test non-existent path.
	if isExecutableFile(filepath.Join(dir, "nonexistent")) {
		t.Error("isExecutableFile for nonexistent path = true, want false")
	}

	// Test user-execute only (mode 0100).
	userExecPath := filepath.Join(dir, "userexec")
	if err := os.WriteFile(userExecPath, []byte("#!/bin/bash\n"), 0o100); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFile(userExecPath) {
		t.Errorf("isExecutableFile(%q) = false, want true for mode 0100 (user exec)", userExecPath)
	}

	// Test other-execute only (mode 0001). The file owner cannot execute this
	// file because the execute bit is only for "other" users. This verifies
	// that isExecutableFile uses unix.Access (which checks actual executability)
	// rather than just checking mode bits.
	otherExecPath := filepath.Join(dir, "otherexec")
	if err := os.WriteFile(otherExecPath, []byte("#!/bin/bash\n"), 0o001); err != nil {
		t.Fatal(err)
	}
	// The test process is the file owner, so it should NOT be able to execute
	// a file with only other-execute permission.
	if isExecutableFile(otherExecPath) {
		t.Errorf("isExecutableFile(%q) = true, want false for mode 0001 (other exec only, owner cannot execute)", otherExecPath)
	}
}
