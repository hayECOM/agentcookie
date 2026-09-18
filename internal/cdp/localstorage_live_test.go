//go:build livechrome

package cdp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chromedp/cdproto/runtime"

	"github.com/mvanhorn/agentcookie/internal/chromedirsync"
)

// seedLocalStorage writes items into userDataDir's profile by letting
// Chrome itself own the leveldb, the same way a real browser would have.
func seedLocalStorage(t *testing.T, userDataDir string, items map[string]map[string]string) {
	t.Helper()
	origins := make([]string, 0, len(items))
	for o := range items {
		origins = append(origins, o)
	}
	err := withOriginVisitor(context.Background(), userDataDir, origins, func(ctx context.Context, origin string) error {
		payload, err := json.Marshal(items[origin])
		if err != nil {
			return err
		}
		js := `(() => { const d = ` + string(payload) + `; for (const k of Object.keys(d)) localStorage.setItem(k, d[k]); return localStorage.length; })()`
		_, _, err = runtime.Evaluate(js).WithReturnByValue(true).Do(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("seedLocalStorage(%s): %v", userDataDir, err)
	}
}

// readLocalStorage reads back what a profile holds for each origin.
func readLocalStorage(t *testing.T, userDataDir string, origins []string) map[string]map[string]string {
	t.Helper()
	got, err := readOriginsFromProfile(context.Background(), userDataDir, origins)
	if err != nil {
		t.Fatalf("readLocalStorage(%s): %v", userDataDir, err)
	}
	return got
}

// packProfileLocalStorage tars a profile's leveldb the way the source
// does before putting it on the wire.
func packProfileLocalStorage(t *testing.T, userDataDir string) []byte {
	t.Helper()
	dir := filepath.Join(userDataDir, "Default", "Local Storage", "leveldb")
	tarball, _, err := chromedirsync.Pack(dir, 0)
	if err != nil {
		t.Fatalf("Pack(%s): %v", dir, err)
	}
	if len(tarball) == 0 {
		t.Fatal("packed an empty tarball")
	}
	return tarball
}

// TestLiveInstallLocalStorage_RoundTripIsAdditiveAndHonorsBlocklist is
// the end-to-end guard for the receiver, and it asserts the three
// properties that make it safe to point at a long-lived reader profile:
//
//  1. a synced origin's items actually arrive;
//  2. a blocklisted origin does NOT (no localStorage side door);
//  3. an origin the READER accumulated on its own is still there
//     afterwards -- this is the never-wipe invariant, and it is the one a
//     directory swap (chromedirsync.AtomicReplaceDir) would break.
//
// Guard-revert: swap InstallLocalStorage's per-origin write for an
// unpack-and-replace of the profile's leveldb and (3) goes red.
func TestLiveInstallLocalStorage_RoundTripIsAdditiveAndHonorsBlocklist(t *testing.T) {
	const (
		syncedOrigin  = "https://app.adnova.test"
		blockedOrigin = "https://www.google.test"
		readerOrigin  = "https://reader-only.test"
	)

	// The laptop's browser profile.
	sourceProfile := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(sourceProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	seedLocalStorage(t, sourceProfile, map[string]map[string]string{
		syncedOrigin:  {"auth_token": "synced-token-value", "workspace": "sg"},
		blockedOrigin: {"should_not_travel": "blocked-value"},
	})
	tarball := packProfileLocalStorage(t, sourceProfile)

	// The mini's long-lived reader profile, with its own prior state.
	readerProfile := filepath.Join(t.TempDir(), "reader")
	if err := os.MkdirAll(readerProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	seedLocalStorage(t, readerProfile, map[string]map[string]string{
		readerOrigin: {"accumulated_by_agent_browser": "must-survive"},
	})

	allowHost := func(host string) bool { return host != "www.google.test" }

	res, err := InstallLocalStorage(context.Background(), readerProfile, tarball, allowHost)
	if err != nil {
		t.Fatalf("InstallLocalStorage: %v", err)
	}
	t.Logf("install result: %s", res.Summary())

	if res.OriginsSeen < 2 {
		t.Errorf("OriginsSeen = %d, want at least the 2 seeded origins", res.OriginsSeen)
	}
	if res.OriginsBlocked != 1 {
		t.Errorf("OriginsBlocked = %d, want 1", res.OriginsBlocked)
	}
	if res.OriginsInstalled != 1 {
		t.Errorf("OriginsInstalled = %d, want 1 (the blocked origin must not install)", res.OriginsInstalled)
	}
	if res.ItemsInstalled != 2 {
		t.Errorf("ItemsInstalled = %d, want 2", res.ItemsInstalled)
	}

	after := readLocalStorage(t, readerProfile, []string{syncedOrigin, blockedOrigin, readerOrigin})

	// (1) the synced origin arrived, values intact.
	if got := after[syncedOrigin]["auth_token"]; got != "synced-token-value" {
		t.Errorf("%s auth_token = %q, want the synced value", syncedOrigin, got)
	}
	if got := after[syncedOrigin]["workspace"]; got != "sg" {
		t.Errorf("%s workspace = %q, want \"sg\"", syncedOrigin, got)
	}

	// (2) the blocklisted origin did not.
	if len(after[blockedOrigin]) != 0 {
		t.Errorf("blocklisted origin %s got a localStorage side door: %v", blockedOrigin, after[blockedOrigin])
	}

	// (3) the never-wipe invariant: the reader's own origin survived.
	if got := after[readerOrigin]["accumulated_by_agent_browser"]; got != "must-survive" {
		t.Errorf("reader's own localStorage was destroyed: %s = %q, want \"must-survive\"", readerOrigin, got)
	}
}

// TestLiveInstallLocalStorage_PreservesOtherKeysOnTheSameOrigin: the
// install is a per-key setItem, not a replace, so a key the reader
// already had on a SYNCED origin must not be dropped just because the
// source does not carry it.
func TestLiveInstallLocalStorage_PreservesOtherKeysOnTheSameOrigin(t *testing.T) {
	const origin = "https://app.adnova.test"

	sourceProfile := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(sourceProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	seedLocalStorage(t, sourceProfile, map[string]map[string]string{
		origin: {"auth_token": "fresh"},
	})
	tarball := packProfileLocalStorage(t, sourceProfile)

	readerProfile := filepath.Join(t.TempDir(), "reader")
	if err := os.MkdirAll(readerProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	seedLocalStorage(t, readerProfile, map[string]map[string]string{
		origin: {"auth_token": "stale", "ui_pref_set_by_agent": "dark"},
	})

	if _, err := InstallLocalStorage(context.Background(), readerProfile, tarball, nil); err != nil {
		t.Fatalf("InstallLocalStorage: %v", err)
	}

	after := readLocalStorage(t, readerProfile, []string{origin})
	if got := after[origin]["auth_token"]; got != "fresh" {
		t.Errorf("auth_token = %q, want the synced value to win", got)
	}
	if got := after[origin]["ui_pref_set_by_agent"]; got != "dark" {
		t.Errorf("reader-only key on a synced origin was dropped: got %q, want \"dark\"", got)
	}
}
