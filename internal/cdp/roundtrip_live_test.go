//go:build livechrome

// Live round-trip tests for the CDP injection path. Tagged `livechrome`
// because they spawn a real headless Chrome against a throwaway profile
// dir; `go test ./...` stays hermetic without them.
//
//	go test -tags livechrome ./internal/cdp/...
//
// These are the regression guards for the two sink defects fixed in
// 2026-08: cookies landing host-only (dot stripped off host_key) and
// cookies landing encrypted under Chrome's mock-keychain key, which a
// normally-launched Chrome cannot decrypt and therefore purges.
package cdp

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/mvanhorn/agentcookie/internal/chrome"
)

func requireChrome(t *testing.T) {
	t.Helper()
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	} {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	if _, err := exec.LookPath("google-chrome"); err != nil {
		t.Skip("no Chrome on this machine; skipping live CDP round-trip")
	}
}

// chromeMicros converts a wall-clock time to Chrome's microseconds-since-1601.
func chromeMicros(tm time.Time) int64 {
	return tm.UnixMicro() + 11644473600*1000*1000
}

func liveProbeCookies() []chrome.Cookie {
	exp := chromeMicros(time.Now().Add(24 * time.Hour))
	return []chrome.Cookie{
		{
			HostKey: ".example.com", Name: "agentcookie_domain_probe", Value: "probe-domain",
			Path: "/", ExpiresUTC: exp, IsSecure: 1, IsHTTPOnly: 0,
			HasExpires: 1, IsPersistent: 1, SameSite: -1, SourceScheme: 2, SourcePort: 443,
		},
		{
			HostKey: "example.com", Name: "agentcookie_hostonly_probe", Value: "probe-hostonly",
			Path: "/", ExpiresUTC: exp, IsSecure: 1, IsHTTPOnly: 0,
			HasExpires: 1, IsPersistent: 1, SameSite: -1, SourceScheme: 2, SourcePort: 443,
		},
	}
}

func injectIntoTempProfile(t *testing.T) string {
	t.Helper()
	requireChrome(t)
	profile := filepath.Join(t.TempDir(), "chrome-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := InjectCookies(ctx, profile, liveProbeCookies()); err != nil {
		t.Fatalf("InjectCookies: %v", err)
	}
	return profile
}

type storedRow struct {
	hostKey   string
	name      string
	encrypted []byte
	plaintext string
}

func readStored(t *testing.T, profile string) map[string]storedRow {
	t.Helper()
	dbPath := filepath.Join(profile, "Default", "Cookies")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("no Cookies db written at %s: %v", dbPath, err)
	}
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro&_busy_timeout=2000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT host_key, name, encrypted_value, value FROM cookies WHERE name LIKE 'agentcookie_%probe'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]storedRow{}
	for rows.Next() {
		var r storedRow
		if err := rows.Scan(&r.hostKey, &r.name, &r.encrypted, &r.plaintext); err != nil {
			t.Fatal(err)
		}
		out[r.name] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestLiveInject_PreservesDomainScope guards the dot on host_key. A
// domain cookie (host_key ".example.com") must land as ".example.com",
// not "example.com" -- a host-only row never rides to www.example.com,
// so every subdomain request goes out unauthenticated.
func TestLiveInject_PreservesDomainScope(t *testing.T) {
	stored := readStored(t, injectIntoTempProfile(t))

	dom, ok := stored["agentcookie_domain_probe"]
	if !ok {
		t.Fatalf("domain-scoped probe cookie was not stored at all; got %v", keysOf(stored))
	}
	if dom.hostKey != ".example.com" {
		t.Errorf("domain cookie host_key = %q, want %q (dot stripped => cookie never rides to subdomains)", dom.hostKey, ".example.com")
	}

	host, ok := stored["agentcookie_hostonly_probe"]
	if !ok {
		t.Fatalf("host-only probe cookie was not stored at all; got %v", keysOf(stored))
	}
	if host.hostKey != "example.com" {
		t.Errorf("host-only cookie host_key = %q, want %q (must not gain a dot)", host.hostKey, "example.com")
	}
}

// TestLiveInject_SealsUnderAutomationKey guards the encryption key. The
// seeded profile is read back by an automation launcher (agent-browser,
// Playwright, Puppeteer, chromedp), all of which run Chrome with
// --use-mock-keychain, so rows must be sealed under the key derived from
// "mock_password". Sealing under the machine's real Safe Storage key
// instead looks correct on disk and is purged wholesale -- measured
// 0/5 survivors -- the first time the reader opens the profile.
func TestLiveInject_SealsUnderAutomationKey(t *testing.T) {
	stored := readStored(t, injectIntoTempProfile(t))
	if len(stored) == 0 {
		t.Fatal("no probe cookies were stored")
	}

	mockKey, err := chrome.DeriveAESKey("mock_password")
	if err != nil {
		t.Fatal(err)
	}
	var realKey []byte
	if password, err := chrome.SafeStoragePassword(); err == nil {
		if k, err := chrome.DeriveAESKey(password); err == nil {
			realKey = k
		}
	}

	for name, row := range stored {
		if len(row.encrypted) == 0 {
			t.Errorf("%s: encrypted_value empty (plaintext fallback, value=%q)", name, row.plaintext)
			continue
		}
		if !decryptsUnder(row, mockKey) {
			t.Errorf("%s: row does NOT decrypt under the automation (mock keychain) key the reader will use", name)
		}
		if realKey != nil && decryptsUnder(row, realKey) {
			t.Errorf("%s: row is sealed under the real OS keyring key; an automation reader cannot decrypt it and will purge the profile", name)
		}
	}
}

// decryptsUnder reports whether row.encrypted decrypts under key. Chrome
// 134+ (cookies db meta.version >= 24) prefixes the plaintext with
// SHA256(host_key) before encrypting, so a matching prefix is a
// value-free proof that the key is right.
func decryptsUnder(row storedRow, key []byte) bool {
	plain, ok := aesCBCDecrypt(row.encrypted, key)
	if !ok {
		return false
	}
	want := sha256.Sum256([]byte(row.hostKey))
	if len(plain) >= len(want) && string(plain[:len(want)]) == string(want[:]) {
		return true
	}
	// Pre-App-Bound profiles store the bare value with no prefix.
	return string(plain) == "probe-domain" || string(plain) == "probe-hostonly"
}

// aesCBCDecrypt undoes Chrome's macOS os_crypt: a "v10" tag followed by
// AES-128-CBC with an all-spaces IV and PKCS#7 padding. Reimplemented
// here rather than exported from internal/chrome so the live probe does
// not widen that package's API for test-only use.
func aesCBCDecrypt(encrypted, key []byte) ([]byte, bool) {
	if len(encrypted) < 3 || string(encrypted[:3]) != "v10" {
		return nil, false
	}
	body := encrypted[3:]
	if len(body) == 0 || len(body)%aes.BlockSize != 0 {
		return nil, false
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	out := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(out, body)
	pad := int(out[len(out)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(out) {
		return nil, false
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return nil, false
		}
	}
	return out[:len(out)-pad], true
}

func keysOf(m map[string]storedRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
