package cdp

import (
	"testing"

	"github.com/chromedp/cdproto/network"

	"github.com/mvanhorn/agentcookie/internal/chrome"
)

func existing(rows ...[3]string) []*network.Cookie {
	out := make([]*network.Cookie, 0, len(rows))
	for _, r := range rows {
		out = append(out, &network.Cookie{Domain: r[0], Name: r[1], Path: r[2]})
	}
	return out
}

// gotDeletes renders the delete set as comparable "domain|name|path"
// strings. DeleteCookiesParams fields are exported, so this reads the
// plan without needing a browser.
func gotDeletes(t *testing.T, dels []*network.DeleteCookiesParams) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, d := range dels {
		out[d.Domain+"|"+d.Name+"|"+d.Path] = true
	}
	return out
}

// TestStaleHostOnlyTwins_DeletesShadowedRow is the core of the upgrade
// path: a host-only row the OLD injector wrote is shadowed by the dotted
// row the fixed injector writes. Both coexist (host_key is part of
// Chrome's unique key), and on an apex request the reader sends both with
// the stale one ordered first, so the apex authenticates with a dead
// token. The stale row has to go.
func TestStaleHostOnlyTwins_DeletesShadowedRow(t *testing.T) {
	batch := []chrome.Cookie{{HostKey: ".example.com", Name: "sess", Path: "/"}}
	dels := staleHostOnlyTwins(existing([3]string{"example.com", "sess", "/"}), batch)
	got := gotDeletes(t, dels)
	if len(got) != 1 || !got["example.com|sess|/"] {
		t.Errorf("expected the shadowed host-only row to be deleted, got %v", got)
	}
}

// TestStaleHostOnlyTwins_KeepsLegitimateTwin is the guard against the
// obvious over-correction. A host-only cookie sharing a name with a
// domain cookie is NOT automatically stale -- the live sink's own source
// carries eight such pairs (OptanonConsent, __cuid, oai_consent_*, ...).
// If the batch writes BOTH, neither may be deleted.
func TestStaleHostOnlyTwins_KeepsLegitimateTwin(t *testing.T) {
	batch := []chrome.Cookie{
		{HostKey: ".example.com", Name: "OptanonConsent", Path: "/"},
		{HostKey: "example.com", Name: "OptanonConsent", Path: "/"},
	}
	dels := staleHostOnlyTwins(existing([3]string{"example.com", "OptanonConsent", "/"}), batch)
	if got := gotDeletes(t, dels); len(got) != 0 {
		t.Errorf("a host-only cookie the source genuinely carries must never be deleted, got %v", got)
	}
}

// TestStaleHostOnlyTwins_KeepsHostPrefixed guards __Host- cookies, which
// buildCookieParam always writes host-only at path "/" even when the
// source row is dotted. Treating one as the dotted half of a twin would
// delete the row we are about to write.
func TestStaleHostOnlyTwins_KeepsHostPrefixed(t *testing.T) {
	batch := []chrome.Cookie{{HostKey: ".github.com", Name: "__Host-user_session_same_site", Path: "/app"}}
	dels := staleHostOnlyTwins(existing([3]string{"github.com", "__Host-user_session_same_site", "/"}), batch)
	if got := gotDeletes(t, dels); len(got) != 0 {
		t.Errorf("__Host- cookies are written host-only and must not be converged away, got %v", got)
	}
}

// TestStaleHostOnlyTwins_IgnoresUnrelatedRows keeps the delete set from
// widening: rows this batch says nothing about, and rows that are
// already domain-scoped, are none of our business.
func TestStaleHostOnlyTwins_IgnoresUnrelatedRows(t *testing.T) {
	batch := []chrome.Cookie{{HostKey: ".example.com", Name: "sess", Path: "/"}}
	dels := staleHostOnlyTwins(existing(
		[3]string{"unrelated.com", "sess", "/"},   // different host
		[3]string{"example.com", "other", "/"},    // different name
		[3]string{"example.com", "sess", "/deep"}, // different path
		[3]string{".example.com", "sess", "/"},    // already dotted
	), batch)
	if got := gotDeletes(t, dels); len(got) != 0 {
		t.Errorf("delete set must not widen beyond the exact shadowed row, got %v", got)
	}
}

// TestStaleHostOnlyTwins_ConvergesOnce proves the steady state: once the
// stale row is gone, a later sync of the same batch plans no deletes, so
// this costs one Storage.getCookies per sync rather than a delete per
// domain cookie forever.
func TestStaleHostOnlyTwins_ConvergesOnce(t *testing.T) {
	batch := []chrome.Cookie{{HostKey: ".example.com", Name: "sess", Path: "/"}}
	converged := existing([3]string{".example.com", "sess", "/"})
	if got := gotDeletes(t, staleHostOnlyTwins(converged, batch)); len(got) != 0 {
		t.Errorf("a converged profile must plan no deletes, got %v", got)
	}
}

// TestStaleHostOnlyTwins_EmptyPathNormalizes covers source rows with an
// empty path, which buildCookieParam and Chrome both treat as "/".
func TestStaleHostOnlyTwins_EmptyPathNormalizes(t *testing.T) {
	batch := []chrome.Cookie{{HostKey: ".example.com", Name: "sess", Path: ""}}
	got := gotDeletes(t, staleHostOnlyTwins(existing([3]string{"example.com", "sess", "/"}), batch))
	if len(got) != 1 || !got["example.com|sess|/"] {
		t.Errorf("empty source path must normalize to / when matching twins, got %v", got)
	}
}
