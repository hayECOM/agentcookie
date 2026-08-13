//go:build livechrome

// Live round-trip tests for the CDP injection path. Tagged `livechrome`
// because they spawn a real headless Chrome against a throwaway profile
// dir; `go test ./...` stays hermetic without them.
//
//	go test -tags livechrome ./internal/cdp/...
//
// These are the regression guards for the three sink defects fixed in
// 2026-08:
//
//  1. Domain cookies landed host-only because the injector stripped the
//     leading dot off host_key, so they never rode to any subdomain.
//  2. Cookies were sealed under the WRONG os_crypt key. Note the
//     direction, because the first attempt at this fix had it backwards
//     and purged the live profile: the reader is an automation launcher
//     running Chrome with --use-mock-keychain, so sealing under the mock
//     key is CORRECT and required. Sealing under the machine's real OS
//     keyring is what destroys the profile. allocator.go carries the
//     measurements; do not "fix" osCryptFlags away.
//  3. Stale host-only rows written by the pre-fix injector survived
//     alongside the corrected dotted rows and shadowed them on apex
//     requests. InjectCookies now converges them away.
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

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

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
// --use-mock-keychain / --password-store=basic, so rows must be sealed
// under the key that pairing produces on THIS platform (see
// automationSealSecret -- macOS and Linux differ in both secret and
// round count). Sealing under the machine's real OS keyring instead
// looks correct on disk and is purged wholesale -- measured 0/5
// survivors -- the first time the reader opens the profile.
func TestLiveInject_SealsUnderAutomationKey(t *testing.T) {
	stored := readStored(t, injectIntoTempProfile(t))
	if len(stored) == 0 {
		t.Fatal("no probe cookies were stored")
	}

	mockKey := automationSealKey(t)
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

// TestLiveInject_ConvergesStaleHostOnlyTwin is the upgrade-path guard,
// end to end against a real Chrome. It seeds a profile the way the
// PRE-FIX injector did -- domain cookies written host-only, dot stripped
// -- then runs the real InjectCookies over it and asserts the stale
// host-only row is gone rather than coexisting with the dotted one.
//
// Without convergence both rows survive, and an apex request sends both
// with the stale one ordered first (RFC 6265 orders by creation time),
// so the apex authenticates with a dead token while subdomains work.
func TestLiveInject_ConvergesStaleHostOnlyTwin(t *testing.T) {
	requireChrome(t)
	profile := filepath.Join(t.TempDir(), "chrome-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}

	// Seed as the OLD injector would: dot stripped, so host-only. Also
	// seed a legitimate host-only cookie whose name is shared with a
	// domain cookie in the batch -- it must survive.
	seedAsOldInjector(t, profile, []*network.CookieParam{
		cookieParam("agentcookie_domain_probe", "stale-apex-token", "", "https://example.com/"),
		cookieParam("agentcookie_legit_twin", "legit-host-only", "", "https://example.com/"),
	})
	before := readAll(t, profile)
	if before[twinKey{"example.com", "agentcookie_domain_probe", "/"}] == "" {
		t.Fatalf("seed failed: no stale host-only row to converge, got %v", before)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	batch := append(liveProbeCookies(),
		chrome.Cookie{
			HostKey: ".example.com", Name: "agentcookie_legit_twin", Value: "v",
			Path: "/", IsSecure: 1, SameSite: -1, SourceScheme: 2, SourcePort: 443,
		},
		chrome.Cookie{
			HostKey: "example.com", Name: "agentcookie_legit_twin", Value: "v",
			Path: "/", IsSecure: 1, SameSite: -1, SourceScheme: 2, SourcePort: 443,
		},
	)
	if err := InjectCookies(ctx, profile, batch); err != nil {
		t.Fatalf("InjectCookies: %v", err)
	}

	after := readAll(t, profile)
	if _, still := after[twinKey{"example.com", "agentcookie_domain_probe", "/"}]; still {
		t.Errorf("stale host-only twin survived the inject; it shadows the dotted row on apex requests")
	}
	if _, ok := after[twinKey{".example.com", "agentcookie_domain_probe", "/"}]; !ok {
		t.Errorf("the dotted row is missing after convergence -- the delete took the wrong row")
	}
	// The legitimate pair the source genuinely carries must both remain.
	for _, host := range []string{"example.com", ".example.com"} {
		if _, ok := after[twinKey{host, "agentcookie_legit_twin", "/"}]; !ok {
			t.Errorf("legitimate %q twin was destroyed by convergence", host)
		}
	}
}

// seedAsOldInjector writes params into profile with a plain Chrome
// session, standing in for a profile written by the pre-fix injector.
func seedAsOldInjector(t *testing.T, profile string, params []*network.CookieParam) {
	t.Helper()
	opts := append(injectorAllocatorOptions(), chromedp.UserDataDir(profile))
	allocCtx, c1 := chromedp.NewExecAllocator(context.Background(), opts...)
	defer c1()
	ctx, c2 := chromedp.NewContext(allocCtx)
	defer c2()
	if err := chromedp.Run(ctx, network.SetCookies(params)); err != nil {
		t.Fatalf("seed SetCookies: %v", err)
	}
	if err := chromedp.Cancel(ctx); err != nil {
		t.Fatalf("seed close: %v", err)
	}
}

func cookieParam(name, value, domain, url string) *network.CookieParam {
	exp := cdp.TimeSinceEpoch(time.Now().Add(48 * time.Hour))
	p := &network.CookieParam{Name: name, Value: value, URL: url, Path: "/", Secure: true, Expires: &exp}
	if domain != "" {
		p.Domain = domain
	}
	return p
}

// readAll returns every row in the profile's Cookies db keyed by
// host_key/name/path. Values are never read.
func readAll(t *testing.T, profile string) map[twinKey]string {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(profile, "Default", "Cookies")+"?mode=ro&_busy_timeout=2000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT host_key, name, path FROM cookies`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[twinKey]string{}
	for rows.Next() {
		var k twinKey
		if err := rows.Scan(&k.host, &k.name, &k.path); err != nil {
			t.Fatal(err)
		}
		out[k] = k.host
	}
	return out
}

func keysOf(m map[string]storedRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
