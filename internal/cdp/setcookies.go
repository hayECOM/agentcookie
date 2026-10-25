package cdp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"

	"github.com/mvanhorn/agentcookie/internal/chrome"
)

// chromeStartTimeout caps how long we wait for the headless Chrome to
// boot and the CDP target to attach. A cold spawn on a Mac mini takes
// roughly 1-2s; 15s is generous headroom for sluggish machines.
const chromeStartTimeout = 15 * time.Second

// InjectCookies spawns a headless Chrome via chromedp against the
// agentcookie-owned profile at profileDir, calls Network.SetCookies
// with the synced cookies (stripping the 32-byte Chrome 127+
// App-Bound prefix from each value first), then closes. One-shot per
// /sync: no persistent Chrome between calls. Chrome encrypts its own
// SQLite with its own Safe Storage key; agentcookie never reads
// Chrome's Keychain item on this path.
//
// The profileDir must already exist (the wizard install creates it
// at install time so we fail loud on misconfiguration rather than
// silently creating an unexpected dir at sync time).
func InjectCookies(ctx context.Context, profileDir string, cookies []chrome.Cookie) error {
	if profileDir == "" {
		return fmt.Errorf("cdp.InjectCookies: profileDir is required")
	}
	expanded := expandHome(profileDir)
	if _, err := os.Stat(expanded); err != nil {
		return fmt.Errorf("cdp.InjectCookies: profile dir %q does not exist: %w (run `agentcookie wizard install --as sink` to create it)", profileDir, err)
	}
	// An already-expired cookie set via Network.setCookies is a delete:
	// it would wipe the live row the reader minted for itself. Skip them
	// before the write (chrome.IsExpiredAt). The twin convergence still
	// sees the full source set, so a host-only row the source carries only
	// in expired form keeps protecting the reader's own live twin.
	source := cookies
	cookies, _ = chrome.DropExpiredForPush(cookies, time.Now())
	if len(cookies) == 0 {
		return nil
	}

	opts := append(injectorAllocatorOptions(), chromedp.UserDataDir(expanded))
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	startCtx, startCancel := context.WithTimeout(allocCtx, chromeStartTimeout)
	defer startCancel()

	chromeCtx, chromeCancel := chromedp.NewContext(startCtx)
	defer chromeCancel()

	// Cookie values arrive here ALREADY stripped of Chrome 127+'s
	// App-Bound 32-byte SHA256(host_key) prefix -- internal/chrome/read.go
	// runs the defensive stripAppBoundPrefix on the source side before
	// shipping. A second strip on this path (as we did in v0.12.0-beta.3)
	// silently lopped 32 bytes off real cookie values for any cookie
	// longer than the prefix, and Chrome rejected the mangled cookies on
	// Network.setCookies -- producing the 64% drop rate measured in the
	// 2026-05-21 dry-run. We pass through c.Value verbatim.
	params := make([]*network.CookieParam, 0, len(cookies))
	for _, c := range cookies {
		params = append(params, buildCookieParam(c, c.Value))
	}

	// Converge away stale host-only twins BEFORE writing. See
	// staleHostOnlyTwins: host_key is part of Chrome's cookie unique key,
	// so a dotless row left behind by an older injector coexists with the
	// dotted row we are about to write instead of being replaced by it.
	// Deleting first (rather than after) means a legitimate host-only
	// cookie can never be left missing by a partial failure -- the delete
	// set already excludes everything this batch writes host-only, and
	// anything we do delete is re-created by the SetCookies below if the
	// source still carries it.
	if err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		existing, err := storage.GetCookies().Do(ctx)
		if err != nil {
			return fmt.Errorf("Storage.getCookies: %w", err)
		}
		for _, del := range staleHostOnlyTwins(existing, cookies, source) {
			if err := del.Do(ctx); err != nil {
				return fmt.Errorf("Network.deleteCookies: %w", err)
			}
		}
		return nil
	})); err != nil {
		return fmt.Errorf("cdp.InjectCookies: converge host-only twins (profile=%s): %w", profileDir, err)
	}

	if err := chromedp.Run(chromeCtx, network.SetCookies(params)); err != nil {
		return fmt.Errorf("cdp.InjectCookies: Network.SetCookies (%d cookies, profile=%s): %w", len(params), profileDir, err)
	}

	// Gracefully close the browser and WAIT for it to exit before
	// returning. Cookies set via Network.setCookies live in Chrome's
	// in-memory cookie store and are only flushed to the profile's SQLite
	// on a clean shutdown. The deferred context cancels below SIGKILL the
	// process, which loses un-flushed writes nondeterministically and
	// leaves the profile's SingletonLock held -- which corrupts the seed
	// when a persistent Chrome is then launched on the same dir (the
	// debug-profile fallback) and can silently drop cookies on the sink.
	// chromedp.Cancel performs the graceful Browser.close and waits for
	// the process to exit, guaranteeing the flush and lock release.
	if err := chromedp.Cancel(chromeCtx); err != nil {
		return fmt.Errorf("cdp.InjectCookies: graceful close (flush): %w", err)
	}
	return nil
}

// twinKey identifies a cookie the way Chrome's storage does for our
// purposes: host, name and path. host is the host_key form WITHOUT a
// leading dot, so a domain row and its host-only twin share a key.
type twinKey struct {
	host string
	name string
	path string
}

// staleHostOnlyTwins returns the deletes needed to converge a profile
// that an OLDER injector wrote.
//
// host_key is part of Chrome's cookie unique key, so "example.com" and
// ".example.com" are two distinct rows, not one row being updated. The
// pre-fix injector stripped the leading dot and wrote domain cookies as
// host-only; the fixed injector writes the dotted row. Because the sink's
// profile is long-lived and never wiped, both then coexist -- and on an
// apex request the reader sends BOTH, with the older (stale) one ordered
// first per RFC 6265. Apex requests therefore authenticate with a dead or
// rotated token while subdomains work: the exact inverse of the scoping
// bug, and it persists until the stale row expires on its own.
//
// The convergence has to be surgical, because a host-only cookie sharing
// a name with a domain cookie is NOT automatically stale -- it is a
// common real pattern (consent and analytics cookies especially), and
// this sink's own source carries eight such legitimate pairs. So a row is
// deleted only when all of these hold:
//
//   - it already exists in the profile (nothing speculative), and
//   - it is host-only (no leading dot), and
//   - this batch does NOT write that same host-only row, and
//   - this batch DOES write the dotted row that shadows it.
//
// Chrome's Network.deleteCookies matches host_key exactly, so passing the
// dotless domain removes the host-only row and leaves the dotted one --
// verified against Chrome 151.
//
// After one pass the profile has converged and this returns nothing, so
// the steady-state cost is a single Storage.getCookies per sync.
//
// source is the full set the source sent, of which batch is the part being
// written (expired rows are skipped). A host-only identity anywhere in
// source still protects the reader's twin: the source genuinely carries that
// cookie, it is just not fresh enough to push this time.
func staleHostOnlyTwins(existing []*network.Cookie, batch, source []chrome.Cookie) []*network.DeleteCookiesParams {
	writesHostOnly := map[twinKey]bool{}
	writesDotted := map[twinKey]bool{}
	for _, c := range source {
		if k, hostOnly, ok := batchTwinKey(c); ok && hostOnly {
			writesHostOnly[k] = true
		}
	}
	for _, c := range batch {
		k, hostOnly, ok := batchTwinKey(c)
		if !ok {
			continue
		}
		if hostOnly {
			writesHostOnly[k] = true
		} else {
			writesDotted[k] = true
		}
	}

	var deletes []*network.DeleteCookiesParams
	seen := map[twinKey]bool{}
	for _, e := range existing {
		if e == nil || strings.HasPrefix(e.Domain, ".") {
			continue
		}
		k := twinKey{e.Domain, e.Name, e.Path}
		if seen[k] || writesHostOnly[k] || !writesDotted[k] {
			continue
		}
		seen[k] = true
		deletes = append(deletes, network.DeleteCookies(k.name).WithDomain(k.host).WithPath(k.path))
	}
	return deletes
}

// batchTwinKey returns the twinKey a source row is written under, and
// whether it is written host-only.
func batchTwinKey(c chrome.Cookie) (twinKey, bool, bool) {
	host := strings.TrimPrefix(c.HostKey, ".")
	if host == "" || c.Name == "" {
		return twinKey{}, false, false
	}
	path := c.Path
	if path == "" {
		path = "/"
	}
	// Mirror buildCookieParam: __Host- cookies are always written
	// host-only at path "/", whatever the source row says, so they
	// must never be treated as the dotted half of a twin.
	if strings.HasPrefix(c.Name, "__Host-") {
		return twinKey{host, c.Name, "/"}, true, true
	}
	return twinKey{host, c.Name, path}, !strings.HasPrefix(c.HostKey, "."), true
}

// buildCookieParam translates a chrome.Cookie row into a CDP
// CookieParam. Two correctness moves matter here:
//
//  1. Synthesize a URL for every cookie. Chrome's Network.setCookies
//     applies stricter validation when only Domain+Path is given
//     (SameSite defaults to Lax which rejects originally cross-site
//     cookies, host-only vs subdomain semantics flake). With URL set,
//     Chrome treats the cookie as if a real navigation set it.
//
//  2. Pass Domain through with its leading "." intact. Chrome's cookies
//     SQLite stores host_key as ".instacart.com" to mark parent-domain
//     scope (valid for all subdomains) and "instacart.com" for
//     host-only. Network.setCookies preserves that distinction verbatim,
//     so the dot must ride along: a dotless Domain -- with or without a
//     URL, and regardless of the URL's host -- lands as a HOST-ONLY row,
//     which never rides to www.instacart.com. That is the 2026-08 sink
//     bug: 1762 of 1883 domain-scoped cookies arrived host-only, so
//     every subdomain request went out unauthenticated even when the
//     rows were otherwise readable. Verified against Chrome 151 in
//     TestLiveInject_PreservesDomainScope.
func buildCookieParam(c chrome.Cookie, value string) *network.CookieParam {
	p := &network.CookieParam{
		Name:     c.Name,
		Value:    value,
		URL:      synthesizeCookieURL(c),
		Path:     c.Path,
		Secure:   c.IsSecure == 1,
		HTTPOnly: c.IsHTTPOnly == 1,
		SameSite: chromeSameSiteToCDP(c.SameSite),
		Expires:  cookieExpiresEpoch(c.ExpiresUTC),
	}

	// Domain attribute: only set it for cookies Chrome stored as
	// domain-scoped (host_key WITH a leading dot, valid for subdomains).
	// Host-only cookies must carry NO Domain -- Chrome scopes them to the
	// exact host via the URL. Two cases are host-only:
	//   1. host_key without a leading dot (Chrome's host-only form).
	//   2. __Host--prefixed cookies, which Network.setCookies HARD-REJECTS
	//      when a Domain is present. Modern host-bound session cookies
	//      (e.g. GitHub's __Host-user_session_same_site) only land when
	//      Domain is omitted.
	// Setting both URL and a Domain on a host-only cookie is also what
	// silently dropped host-bound login cookies before this fix.
	isHostPrefixed := strings.HasPrefix(c.Name, "__Host-")
	if strings.HasPrefix(c.HostKey, ".") && !isHostPrefixed {
		p.Domain = c.HostKey
	}

	// __Host- cookies have mandatory attributes (Secure, Path "/"); enforce
	// them so a slightly-off source row is not rejected wholesale.
	if isHostPrefixed {
		p.Secure = true
		p.Path = "/"
	}

	return p
}

// synthesizeCookieURL builds a request-URI for a cookie from its
// host_key and path. Chrome cookies record host_key as either
// ".example.com" (suffix match, valid for subdomains) or "example.com"
// (exact match, host-only). For URL purposes we always need a real
// hostname, so we strip the leading dot if present.
func synthesizeCookieURL(c chrome.Cookie) string {
	host := c.HostKey
	if len(host) > 0 && host[0] == '.' {
		host = host[1:]
	}
	if host == "" {
		return ""
	}
	scheme := "https"
	if c.IsSecure == 0 {
		scheme = "http"
	}
	path := c.Path
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}

// chromeSameSiteToCDP translates Chrome's numeric SameSite encoding
// (stored in cookies.samesite) to the CDP CookieSameSite enum. Without
// this, missing SameSite causes Chrome to default to Lax on the CDP
// path, which rejects cookies that were originally cross-site.
//
// Chrome encoding:
//
//	-1 = unspecified
//	 0 = None
//	 1 = Lax
//	 2 = Strict
func chromeSameSiteToCDP(s int) network.CookieSameSite {
	switch s {
	case 0:
		return network.CookieSameSiteNone
	case 1:
		return network.CookieSameSiteLax
	case 2:
		return network.CookieSameSiteStrict
	default:
		// -1 / unspecified: emit empty so chromedp omits the field and
		// Chrome uses its own default behavior for unspecified.
		return ""
	}
}

// cookieExpiresEpoch converts Chrome's microseconds-since-1601 cookie
// expiry to a chromedp/cdproto.TimeSinceEpoch (seconds-since-Unix
// epoch as a float). Cookies with ExpiresUTC = 0 are session cookies
// and we leave Expires nil so Chrome treats them as session-scoped.
func cookieExpiresEpoch(chromeMicrosSince1601 int64) *cdp.TimeSinceEpoch {
	if chromeMicrosSince1601 == 0 {
		return nil
	}
	// Chrome epoch: 1601-01-01 UTC. Unix epoch: 1970-01-01 UTC.
	// Offset is 11644473600 seconds. Chrome stores microseconds.
	const chromeEpochOffsetSec = 11644473600
	unixSec := float64(chromeMicrosSince1601)/1e6 - chromeEpochOffsetSec
	t := time.Unix(int64(unixSec), 0)
	te := cdp.TimeSinceEpoch(t)
	return &te
}

// expandHome resolves a leading ~/ in a path against the current
// user's home directory.
func expandHome(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}
