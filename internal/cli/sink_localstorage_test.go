package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mvanhorn/agentcookie/internal/cdp"
	"github.com/mvanhorn/agentcookie/internal/chrome"
	"github.com/mvanhorn/agentcookie/internal/config"
	"github.com/mvanhorn/agentcookie/internal/protocol"
	"github.com/mvanhorn/agentcookie/internal/state"
	"github.com/mvanhorn/agentcookie/internal/transport"
)

// lsFixture is a /sync harness that lets a test set the sink config and
// put a localStorage tarball on the wire, which the shared fixture in
// sink_test.go does not.
type lsFixture struct {
	mux    *http.ServeMux
	secret string
}

func newLSFixture(t *testing.T, cfg *config.SinkConfig) *lsFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	withConfigDir(t, t.TempDir())

	if cfg.Listen.Addr == "" {
		cfg.Listen.Addr = "127.0.0.1:9999"
	}
	secret := "localstorage-test-secret"
	stateWriter := state.NewWriter(filepath.Join(t.TempDir(), "sink-state.json"))
	sinkState := &state.SinkState{Role: "sink", ListenAddr: cfg.Listen.Addr}
	var stateMu sync.Mutex
	mux := newSinkMux(cfg, secret, []byte("0123456789abcdef"), protocol.NewSequenceTracker(), stateWriter, sinkState, &stateMu)
	return &lsFixture{mux: mux, secret: secret}
}

func (f *lsFixture) post(t *testing.T, seq int64, cookies []chrome.Cookie, lsTarball []byte) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(protocol.SyncEnvelope{
		ProtocolVersion:     protocol.Version,
		SourceHostname:      "laptop-test",
		Sequence:            seq,
		Cookies:             cookies,
		LocalStorageTarball: lsTarball,
	})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := transport.SealWithSecret(payload, f.secret)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(sealed)))
	return rec
}

func testCookies() []chrome.Cookie {
	return []chrome.Cookie{{HostKey: ".example.com", Name: "sid", Value: "v", Path: "/"}}
}

// TestSinkSkipMode_DoesNotScanThisMachinesBrowsers is the guard for the
// extra-profile gate. A skip_chrome_sqlite sink exists to never touch
// Chrome Safe Storage; running the union there probes the Keychain once
// per browser per /sync, inside the request handler.
//
// Guard-revert: drop the `if !cfg.SkipChromeSQLite` around the union
// call and this goes red.
func TestSinkSkipMode_DoesNotScanThisMachinesBrowsers(t *testing.T) {
	called := false
	restore := SetExtraProfileUnionForTesting(func(c []chrome.Cookie, _ string, _ *protocol.BlocklistMatcher) []chrome.Cookie {
		called = true
		return c
	})
	defer restore()

	f := newLSFixture(t, &config.SinkConfig{SkipChromeSQLite: true})
	if rec := f.post(t, 1, testCookies(), nil); rec.Code != http.StatusOK {
		t.Fatalf("/sync = %d, body %q", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("skip_chrome_sqlite sink ran the extra-profile union; it must never reach for Safe Storage")
	}
}

// TestSinkUniversalMode_StillUnionsExtraProfiles: the gate must not
// change behavior for a normal (universal-delivery) sink, which is the
// install upstream ships.
func TestSinkUniversalMode_StillUnionsExtraProfiles(t *testing.T) {
	called := false
	restore := SetExtraProfileUnionForTesting(func(c []chrome.Cookie, _ string, _ *protocol.BlocklistMatcher) []chrome.Cookie {
		called = true
		return c
	})
	defer restore()

	// An empty cookie set keeps applyEnvelopeToSink off the SQLite path
	// (nothing to write) while still reaching the union, which upstream
	// runs even for an empty envelope so adapters see extra-profile
	// cookies either way.
	f := newLSFixture(t, &config.SinkConfig{
		Chrome: config.ChromeRef{DBPath: filepath.Join(t.TempDir(), "Cookies")},
	})
	if rec := f.post(t, 1, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("/sync = %d, body %q", rec.Code, rec.Body.String())
	}

	if !called {
		t.Error("a universal-delivery sink must still union extra profiles")
	}
}

// TestSinkLocalStorage_OffByDefault: the receiver is opt-in, so a
// sink.yaml written before this feature cannot have its cookie path
// destabilized by a new writer it never asked for.
func TestSinkLocalStorage_OffByDefault(t *testing.T) {
	called := false
	restore := SetLocalStorageInstallerForTesting(func(context.Context, string, []byte, func(string) bool) (cdp.LocalStorageResult, error) {
		called = true
		return cdp.LocalStorageResult{}, nil
	})
	defer restore()

	f := newLSFixture(t, &config.SinkConfig{
		SkipChromeSQLite: true,
		CDP:              config.CDPRef{Enabled: true, ProfileDir: t.TempDir()},
	})
	f.post(t, 1, testCookies(), []byte("a tarball"))

	if called {
		t.Error("localStorage installed without cdp.local_storage set")
	}
}

// TestSinkLocalStorage_InstallsWhenEnabled covers the wiring: config on
// + a payload on the wire reaches the installer, with the cookie
// blocklist handed through as the allow predicate.
func TestSinkLocalStorage_InstallsWhenEnabled(t *testing.T) {
	var gotTarball []byte
	var gotAllow func(string) bool
	restore := SetLocalStorageInstallerForTesting(func(_ context.Context, _ string, tb []byte, allow func(string) bool) (cdp.LocalStorageResult, error) {
		gotTarball = tb
		gotAllow = allow
		return cdp.LocalStorageResult{OriginsInstalled: 2, ItemsInstalled: 5}, nil
	})
	defer restore()

	f := newLSFixture(t, &config.SinkConfig{
		SkipChromeSQLite: true,
		CDP:              config.CDPRef{Enabled: true, ProfileDir: t.TempDir(), LocalStorage: true},
	})
	rec := f.post(t, 1, testCookies(), []byte("a tarball"))

	if rec.Code != http.StatusOK {
		t.Fatalf("/sync = %d, body %q", rec.Code, rec.Body.String())
	}
	if string(gotTarball) != "a tarball" {
		t.Errorf("installer got tarball %q, want the envelope's", string(gotTarball))
	}
	if gotAllow == nil {
		t.Error("installer was not handed the cookie blocklist predicate")
	}
	if body := rec.Body.String(); !bytes.Contains([]byte(body), []byte("localStorage install")) {
		t.Errorf("ok-line %q should report the install back to the source", body)
	}
}

// TestSinkLocalStorage_SkipsUnchangedTarball: this runs after every
// /sync (~30s in production). Re-installing a byte-identical tarball
// would spend two Chrome spawns a minute rewriting the same bytes.
func TestSinkLocalStorage_SkipsUnchangedTarball(t *testing.T) {
	installs := 0
	restore := SetLocalStorageInstallerForTesting(func(context.Context, string, []byte, func(string) bool) (cdp.LocalStorageResult, error) {
		installs++
		return cdp.LocalStorageResult{OriginsInstalled: 1}, nil
	})
	defer restore()

	f := newLSFixture(t, &config.SinkConfig{
		SkipChromeSQLite: true,
		CDP:              config.CDPRef{Enabled: true, ProfileDir: t.TempDir(), LocalStorage: true},
	})

	f.post(t, 1, testCookies(), []byte("tarball-v1"))
	f.post(t, 2, testCookies(), []byte("tarball-v1"))
	if installs != 1 {
		t.Errorf("installs = %d after two identical tarballs, want 1", installs)
	}

	f.post(t, 3, testCookies(), []byte("tarball-v2"))
	if installs != 2 {
		t.Errorf("installs = %d after a changed tarball, want 2", installs)
	}
}

// TestSinkLocalStorage_FailureDoesNotBreakTheCookiePath: the cookie
// lane is load-bearing daily; a new writer must not be able to fail the
// /sync that already delivered cookies to the sidecar.
func TestSinkLocalStorage_FailureDoesNotBreakTheCookiePath(t *testing.T) {
	restore := SetLocalStorageInstallerForTesting(func(context.Context, string, []byte, func(string) bool) (cdp.LocalStorageResult, error) {
		return cdp.LocalStorageResult{}, context.DeadlineExceeded
	})
	defer restore()

	f := newLSFixture(t, &config.SinkConfig{
		SkipChromeSQLite: true,
		CDP:              config.CDPRef{Enabled: true, ProfileDir: t.TempDir(), LocalStorage: true},
	})
	rec := f.post(t, 1, testCookies(), []byte("a tarball"))

	if rec.Code != http.StatusOK {
		t.Fatalf("a localStorage failure failed the whole sync: %d %q", rec.Code, rec.Body.String())
	}
}

// TestSinkSync_NeverPushesExpiredCookies is the guard for the 2026-10-25
// Apple Add to Bag 541: the laptop's expired shield cookies rode every
// /sync into the browser, and a cookie set with a past expiry is a delete.
// The push surfaces must get only unexpired cookies (session cookies
// included); the sidecar mirror is not a push and is not asserted here.
//
// Guard-revert: hand cdpInject `cookies` instead of `pushable` in the
// /sync handler and this goes red.
func TestSinkSync_NeverPushesExpiredCookies(t *testing.T) {
	var injected []chrome.Cookie
	restore := SetCDPInjectorForTesting(func(_ context.Context, _ string, c []chrome.Cookie) error {
		injected = append(injected, c...)
		return nil
	})
	defer restore()

	f := newLSFixture(t, &config.SinkConfig{
		SkipChromeSQLite: true,
		CDP:              config.CDPRef{Enabled: true, ProfileDir: t.TempDir()},
	})
	const chromeEpochOffsetMicros = 11644473600 * 1_000_000
	past := time.Now().Add(-36*time.Hour).UnixMicro() + chromeEpochOffsetMicros
	future := time.Now().Add(24*time.Hour).UnixMicro() + chromeEpochOffsetMicros
	rec := f.post(t, 1, []chrome.Cookie{
		{HostKey: ".example.com", Name: "shield", Value: "stale", Path: "/", ExpiresUTC: past},
		{HostKey: ".example.com", Name: "session", Value: "s", Path: "/"},
		{HostKey: ".example.com", Name: "live", Value: "l", Path: "/", ExpiresUTC: future},
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/sync = %d, body %q", rec.Code, rec.Body.String())
	}
	var names []string
	for _, c := range injected {
		names = append(names, c.Name)
	}
	if len(names) != 2 || names[0] != "session" || names[1] != "live" {
		t.Errorf("CDP injector got %v, want [session live] (expired cookie must be skipped)", names)
	}
}
