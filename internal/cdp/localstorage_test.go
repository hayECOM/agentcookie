package cdp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOriginsInLevelDB_SkipsManifest is the guard for the trap that sent
// a whole diagnosis pass at the wrong machine: MANIFEST retains the key
// boundaries of tables that have since been compacted away, so an origin
// named there may no longer be in the store at all. Enumerating from it
// would make the installer chase origins that carry nothing.
func TestOriginsInLevelDB_SkipsManifest(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("000119.ldb", "\x00\x01META:https://app.adnova.ai\x00junk")
	write("000121.log", "garbage META:https://app.triplewhale.com more")
	write("MANIFEST-000001", "META:https://compacted-away.example")
	write("LOG", "META:https://also-not-data.example")
	write("CURRENT", "MANIFEST-000001\n")

	origins, err := originsInLevelDB(dir)
	if err != nil {
		t.Fatalf("originsInLevelDB: %v", err)
	}

	got := strings.Join(origins, ",")
	want := "https://app.adnova.ai,https://app.triplewhale.com"
	if got != want {
		t.Errorf("origins = %q, want %q", got, want)
	}
	for _, o := range origins {
		if strings.Contains(o, "compacted-away") || strings.Contains(o, "also-not-data") {
			t.Errorf("enumerated a non-data file: %q", o)
		}
	}
}

func TestOriginsInLevelDB_DedupesAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.ldb", "b.ldb", "c.log"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("META:https://same.example"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	origins, err := originsInLevelDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(origins) != 1 || origins[0] != "https://same.example" {
		t.Errorf("origins = %v, want exactly [https://same.example]", origins)
	}
}

func TestOriginsInLevelDB_MissingDir(t *testing.T) {
	if _, err := originsInLevelDB(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing leveldb dir")
	}
}

// TestPartitionOrigins_BlocklistHasNoLocalStorageSideDoor is the guard
// for the boundary the dispatch drew: a domain opted out of cookie sync
// must not arrive through localStorage instead.
func TestPartitionOrigins_BlocklistHasNoLocalStorageSideDoor(t *testing.T) {
	blocked := map[string]bool{
		"www.google.com":     true,
		"app.mercury.com":    true,
		"shop.myshopify.com": true,
		"www.facebook.com":   true,
	}
	allowHost := func(h string) bool { return !blocked[h] }

	origins := []string{
		"https://app.adnova.ai",
		"https://www.google.com",
		"https://app.mercury.com",
		"https://shop.myshopify.com",
		"https://www.facebook.com",
		"https://app.triplewhale.com",
	}

	allowed, blockedHosts := partitionOrigins(origins, allowHost)

	wantAllowed := "https://app.adnova.ai,https://app.triplewhale.com"
	if got := strings.Join(allowed, ","); got != wantAllowed {
		t.Errorf("allowed = %q, want %q", got, wantAllowed)
	}
	wantBlocked := "app.mercury.com,shop.myshopify.com,www.facebook.com,www.google.com"
	if got := strings.Join(blockedHosts, ","); got != wantBlocked {
		t.Errorf("blockedHosts = %q, want %q", got, wantBlocked)
	}
}

func TestPartitionOrigins_NilAllowHostAllowsAll(t *testing.T) {
	origins := []string{"https://a.example", "https://b.example"}
	allowed, blocked := partitionOrigins(origins, nil)
	if len(allowed) != 2 {
		t.Errorf("allowed = %v, want both", allowed)
	}
	if len(blocked) != 0 {
		t.Errorf("blocked = %v, want none", blocked)
	}
}

// TestPartitionOrigins_UnparseableHostFailsClosed: an origin whose host
// cannot be extracted cannot be checked against the blocklist, so it
// must be dropped rather than waved through.
func TestPartitionOrigins_UnparseableHostFailsClosed(t *testing.T) {
	origins := []string{"https://ok.example", "not-a-url", "https://"}
	allowed, _ := partitionOrigins(origins, func(string) bool { return true })
	if len(allowed) != 1 || allowed[0] != "https://ok.example" {
		t.Errorf("allowed = %v, want only https://ok.example", allowed)
	}
}

func TestOriginHost(t *testing.T) {
	cases := map[string]string{
		"https://app.adnova.ai":      "app.adnova.ai",
		"http://localhost:3000":      "localhost",
		"https://a.b.c.example:8443": "a.b.c.example",
		"not-a-url":                  "",
		"https://":                   "",
	}
	for in, want := range cases {
		if got := originHost(in); got != want {
			t.Errorf("originHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLocalStorageResult_SummaryNamesEachOutcome: the operator has to be
// able to tell "nothing arrived" from "everything was filtered" from
// "it worked" without reading the code.
func TestLocalStorageResult_SummaryNamesEachOutcome(t *testing.T) {
	r := LocalStorageResult{
		OriginsSeen:      5,
		OriginsBlocked:   2,
		OriginsEmpty:     1,
		OriginsInstalled: 2,
		ItemsInstalled:   17,
		BlockedHosts:     []string{"www.google.com", "app.mercury.com"},
	}
	s := r.Summary()
	for _, want := range []string{"5 origins in tarball", "2 installed", "17 items", "2 blocked", "www.google.com", "1 empty"} {
		if !strings.Contains(s, want) {
			t.Errorf("Summary() = %q, missing %q", s, want)
		}
	}
}

func TestInstallLocalStorage_RequiresProfileDir(t *testing.T) {
	if _, err := InstallLocalStorage(t.Context(), "", []byte("x"), nil); err == nil {
		t.Error("expected an error when profileDir is empty")
	}
}

func TestInstallLocalStorage_MissingProfileDirIsAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created")
	if _, err := InstallLocalStorage(t.Context(), missing, []byte("x"), nil); err == nil {
		t.Error("expected an error when the profile dir does not exist")
	}
}

// TestInstallLocalStorage_EmptyTarballIsANoop: an envelope with no
// localStorage must not spawn Chrome or report a failure.
func TestInstallLocalStorage_EmptyTarballIsANoop(t *testing.T) {
	dir := t.TempDir()
	res, err := InstallLocalStorage(t.Context(), dir, nil, nil)
	if err != nil {
		t.Fatalf("empty tarball should be a no-op, got %v", err)
	}
	if res.OriginsSeen != 0 || res.OriginsInstalled != 0 {
		t.Errorf("empty tarball produced %+v", res)
	}
}
