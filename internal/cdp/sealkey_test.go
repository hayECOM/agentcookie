package cdp

import (
	"crypto/pbkdf2"
	"crypto/sha1"
	"runtime"
	"testing"

	"github.com/mvanhorn/agentcookie/internal/chrome"
)

// Chrome's os_crypt key derivation differs by platform, and the injector
// runs with --use-mock-keychain / --password-store=basic on both. macOS
// takes the mock Keychain path (PBKDF2 1003 rounds over "mock_password");
// Linux's "basic" password store is the no-keyring fallback, which seals
// v10 under a single PBKDF2 round over the hardcoded "peanuts". Same
// salt, same AES-128 key length, different secret AND different round
// count -- so a test that pins the macOS pair false-fails on Linux even
// when the injector is behaving correctly. This repo grew a Linux sink in
// b55e2f0, so the live seal assertion has to branch.
const (
	sealSalt      = "saltysalt"
	sealKeyLen    = 16
	macSealSecret = "mock_password"
	macSealIter   = 1003
	lnxSealSecret = "peanuts"
	lnxSealIter   = 1
)

// automationSealSecret returns the password and PBKDF2 round count Chrome
// uses for cookie sealing on goos when launched by an automation
// allocator (see osCryptFlags).
func automationSealSecret(goos string) (password string, iter int) {
	if goos == "linux" {
		return lnxSealSecret, lnxSealIter
	}
	return macSealSecret, macSealIter
}

// automationSealKey derives the key the reader on THIS platform will use.
func automationSealKey(t *testing.T) []byte {
	t.Helper()
	password, iter := automationSealSecret(runtime.GOOS)
	key, err := pbkdf2.Key(sha1.New, password, []byte(sealSalt), iter, sealKeyLen)
	if err != nil {
		t.Fatalf("derive automation seal key: %v", err)
	}
	return key
}

// TestAutomationSealSecretPerPlatform pins both branches. Without the
// Linux branch, TestLiveInject_SealsUnderAutomationKey reports a
// correctly-sealed Linux profile as broken.
func TestAutomationSealSecretPerPlatform(t *testing.T) {
	cases := []struct {
		goos     string
		password string
		iter     int
	}{
		{"darwin", macSealSecret, macSealIter},
		{"linux", lnxSealSecret, lnxSealIter},
	}
	for _, tc := range cases {
		password, iter := automationSealSecret(tc.goos)
		if password != tc.password || iter != tc.iter {
			t.Errorf("automationSealSecret(%q) = (%q, %d), want (%q, %d)",
				tc.goos, password, iter, tc.password, tc.iter)
		}
	}
}

// TestAutomationSealKeyMatchesChromeDerivation cross-checks the macOS
// branch against internal/chrome's own derivation, so the two cannot
// drift apart silently.
func TestAutomationSealKeyMatchesChromeDerivation(t *testing.T) {
	password, iter := automationSealSecret("darwin")
	got, err := pbkdf2.Key(sha1.New, password, []byte(sealSalt), iter, sealKeyLen)
	if err != nil {
		t.Fatal(err)
	}
	want, err := chrome.DeriveAESKey(macSealSecret)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("macOS seal key does not match internal/chrome.DeriveAESKey")
	}
}
