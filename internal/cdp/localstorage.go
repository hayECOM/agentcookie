package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	cdpbase "github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/mvanhorn/agentcookie/internal/chromedirsync"
)

// LocalStorageResult reports what one InstallLocalStorage call did, in
// the terms an operator needs to tell "nothing arrived" apart from
// "everything was filtered" apart from "it worked".
type LocalStorageResult struct {
	OriginsSeen      int      // origins found in the synced tarball
	OriginsBlocked   int      // dropped by the cookie blocklist
	OriginsEmpty     int      // present in the tarball but holding no items
	OriginsInstalled int      // origins with at least one item written
	ItemsInstalled   int      // total key/value pairs written
	BlockedHosts     []string // sorted, deduped; for the operator log
}

// Summary renders the result as one operator-facing line.
func (r LocalStorageResult) Summary() string {
	s := fmt.Sprintf("%d origins in tarball -> %d installed (%d items)", r.OriginsSeen, r.OriginsInstalled, r.ItemsInstalled)
	if r.OriginsBlocked > 0 {
		s += fmt.Sprintf(", %d blocked by cookie policy (%s)", r.OriginsBlocked, strings.Join(r.BlockedHosts, ", "))
	}
	if r.OriginsEmpty > 0 {
		s += fmt.Sprintf(", %d empty", r.OriginsEmpty)
	}
	return s
}

// metaOriginRe matches Chrome's localStorage LevelDB per-origin metadata
// key, "META:<origin>". Origins are the only thing read out of the raw
// bytes -- the key/value encoding is left to Chrome (see
// readOriginsFromProfile), so a Chrome-side format change cannot silently
// corrupt what we install.
var metaOriginRe = regexp.MustCompile(`META:(https?://[a-zA-Z0-9._\-]+(?::[0-9]{1,5})?)`)

// localStorageTimeout caps one full install (two short-lived Chromes).
const localStorageTimeout = 90 * time.Second

// InstallLocalStorage installs the synced localStorage tarball into the
// reader profile at profileDir, ONE ORIGIN AT A TIME.
//
// It deliberately does NOT unpack the tarball over the profile's own
// leveldb the way the real-Chrome sink path does. That path calls
// chromedirsync.AtomicReplaceDir, which swaps the whole directory: at
// this profile it would delete every origin the reader itself
// accumulated (agent-browser sessions write their own localStorage here)
// and would race agent-browser for the leveldb LOCK. This profile is
// long-lived and never wiped, so the install has to be additive --
// setItem per key, nothing removed, nothing truncated.
//
// allowHost is the cookie blocklist, applied to each origin's host so an
// opted-out domain (mercury/google/shopify/meta) cannot gain a
// localStorage side door. A nil allowHost allows everything.
//
// Values are not logged: localStorage on these origins holds live bearer
// tokens. Only origin names and counts reach stderr.
func InstallLocalStorage(ctx context.Context, profileDir string, tarball []byte, allowHost func(host string) bool) (LocalStorageResult, error) {
	var result LocalStorageResult

	if profileDir == "" {
		return result, fmt.Errorf("cdp.InstallLocalStorage: profileDir is required")
	}
	expanded := expandHome(profileDir)
	if _, err := os.Stat(expanded); err != nil {
		return result, fmt.Errorf("cdp.InstallLocalStorage: profile dir %q does not exist: %w", profileDir, err)
	}
	if len(tarball) == 0 {
		return result, nil
	}

	ctx, cancel := context.WithTimeout(ctx, localStorageTimeout)
	defer cancel()

	// Stage the source leveldb in a scratch user-data-dir. Chrome reads
	// it there; nothing is ever unpacked at or near the reader profile.
	scratch, err := os.MkdirTemp("", "agentcookie-ls-")
	if err != nil {
		return result, fmt.Errorf("cdp.InstallLocalStorage: scratch dir: %w", err)
	}
	defer os.RemoveAll(scratch)

	stagedLevelDB := filepath.Join(scratch, "Default", "Local Storage", "leveldb")
	if err := chromedirsync.Unpack(tarball, stagedLevelDB); err != nil {
		return result, fmt.Errorf("cdp.InstallLocalStorage: unpack tarball: %w", err)
	}

	origins, err := originsInLevelDB(stagedLevelDB)
	if err != nil {
		return result, fmt.Errorf("cdp.InstallLocalStorage: enumerate origins: %w", err)
	}
	result.OriginsSeen = len(origins)
	if len(origins) == 0 {
		return result, nil
	}

	allowed, blockedHosts := partitionOrigins(origins, allowHost)
	result.BlockedHosts = blockedHosts
	result.OriginsBlocked = len(blockedHosts)
	if len(allowed) == 0 {
		return result, nil
	}

	// Pass 1: let Chrome decode its own format.
	items, err := readOriginsFromProfile(ctx, scratch, allowed)
	if err != nil {
		return result, fmt.Errorf("cdp.InstallLocalStorage: read staged profile: %w", err)
	}

	nonEmpty := make(map[string]map[string]string, len(items))
	for origin, kv := range items {
		if len(kv) == 0 {
			result.OriginsEmpty++
			continue
		}
		nonEmpty[origin] = kv
	}
	if len(nonEmpty) == 0 {
		return result, nil
	}

	// Pass 2: write into the reader profile, additively.
	installedOrigins, installedItems, err := writeOriginsToProfile(ctx, expanded, nonEmpty)
	result.OriginsInstalled = installedOrigins
	result.ItemsInstalled = installedItems
	if err != nil {
		return result, fmt.Errorf("cdp.InstallLocalStorage: write reader profile: %w", err)
	}
	return result, nil
}

// originsInLevelDB scans the staged LevelDB's data files for per-origin
// metadata keys. Only .ldb and .log are read: MANIFEST retains key
// boundaries for tables that have since been compacted away, so scanning
// it reports origins the store no longer holds.
func originsInLevelDB(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var origins []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".ldb" && ext != ".log" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, m := range metaOriginRe.FindAllSubmatch(raw, -1) {
			o := string(m[1])
			if seen[o] {
				continue
			}
			seen[o] = true
			origins = append(origins, o)
		}
	}
	sort.Strings(origins)
	return origins, nil
}

// partitionOrigins splits origins into the ones allowHost permits and
// the hosts it rejected (sorted and deduped, for the operator log).
//
// The blocklist is the SAME predicate the cookie path uses. A domain
// opted out there (mercury, google, shopify, meta) must not come back
// through localStorage: for a modern app the localStorage token IS the
// session, so a side door here would quietly undo the cookie-side
// decision. An origin whose host will not parse is dropped rather than
// allowed -- fail closed, since an unparseable host cannot be checked
// against the blocklist at all.
func partitionOrigins(origins []string, allowHost func(string) bool) (allowed []string, blockedHosts []string) {
	blocked := make(map[string]bool)
	for _, o := range origins {
		host := originHost(o)
		if host == "" {
			continue
		}
		if allowHost != nil && !allowHost(host) {
			blocked[host] = true
			continue
		}
		allowed = append(allowed, o)
	}
	for h := range blocked {
		blockedHosts = append(blockedHosts, h)
	}
	sort.Strings(blockedHosts)
	return allowed, blockedHosts
}

// originHost returns the hostname of an origin, or "" if unparseable.
func originHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// readOriginsFromProfile launches one Chrome against userDataDir and
// reads each origin's localStorage as a flat map.
func readOriginsFromProfile(ctx context.Context, userDataDir string, origins []string) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string, len(origins))
	err := withOriginVisitor(ctx, userDataDir, origins, func(ctx context.Context, origin string) error {
		const readJS = `(() => {
			const o = {};
			for (let i = 0; i < localStorage.length; i++) {
				const k = localStorage.key(i);
				o[k] = localStorage.getItem(k);
			}
			return JSON.stringify(o);
		})()`
		res, exc, err := runtime.Evaluate(readJS).WithReturnByValue(true).Do(ctx)
		if err != nil || exc != nil {
			// An origin we cannot read is skipped, not fatal: the rest
			// of the tarball still installs.
			return nil
		}
		if res == nil || res.Value == nil {
			return nil
		}
		var encoded string
		if err := json.Unmarshal(res.Value, &encoded); err != nil {
			return nil
		}
		kv := map[string]string{}
		if err := json.Unmarshal([]byte(encoded), &kv); err != nil {
			return nil
		}
		out[origin] = kv
		return nil
	})
	return out, err
}

// writeOriginsToProfile launches one Chrome against the reader profile
// and setItem()s each origin's pairs. Additive only: no clear(), no
// removeItem(), no directory replacement.
func writeOriginsToProfile(ctx context.Context, userDataDir string, items map[string]map[string]string) (int, int, error) {
	origins := make([]string, 0, len(items))
	for o := range items {
		origins = append(origins, o)
	}
	sort.Strings(origins)

	originCount := 0
	itemCount := 0
	err := withOriginVisitor(ctx, userDataDir, origins, func(ctx context.Context, origin string) error {
		payload, err := json.Marshal(items[origin])
		if err != nil {
			return nil
		}
		// The pairs travel as a JSON literal, never string-concatenated
		// into source: values are attacker-shaped only in the sense that
		// they are arbitrary site data, and one stray quote would
		// otherwise break the whole origin's install.
		writeJS := fmt.Sprintf(`(() => {
			const d = %s;
			let n = 0;
			for (const k of Object.keys(d)) {
				try { localStorage.setItem(k, d[k]); n++; } catch (e) {}
			}
			return n;
		})()`, string(payload))
		res, exc, err := runtime.Evaluate(writeJS).WithReturnByValue(true).Do(ctx)
		if err != nil || exc != nil {
			return nil
		}
		var n int
		if res != nil && res.Value != nil {
			if err := json.Unmarshal(res.Value, &n); err != nil {
				return nil
			}
		}
		if n > 0 {
			originCount++
			itemCount += n
		}
		return nil
	})
	return originCount, itemCount, err
}

// withOriginVisitor launches one headless Chrome against userDataDir,
// navigates to each origin in turn, and runs visit with the page at that
// origin.
//
// Every request is intercepted and fulfilled locally with an empty
// document, so visiting an origin costs no network request: the sink
// must not phone out to app.adnova.ai (or anywhere else) just to seed a
// storage area, and a site that happened to be down would otherwise take
// its own localStorage down with it.
//
// localStorage lives in memory until Chrome shuts down cleanly, exactly
// like the cookie store -- so this closes through chromedp.Cancel and
// waits, never the deferred SIGKILL. Measured: without the graceful
// close, a value written here reads back as null on the next open.
func withOriginVisitor(ctx context.Context, userDataDir string, origins []string, visit func(context.Context, string) error) error {
	opts := append(injectorAllocatorOptions(), chromedp.UserDataDir(userDataDir))
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	startCtx, startCancel := context.WithTimeout(allocCtx, chromeStartTimeout)
	defer startCancel()

	chromeCtx, chromeCancel := chromedp.NewContext(startCtx)
	defer chromeCancel()

	if err := chromedp.Run(chromeCtx); err != nil {
		return fmt.Errorf("start chrome: %w", err)
	}

	stubBody := base64.StdEncoding.EncodeToString([]byte("<!doctype html><title>agentcookie</title>"))
	chromedp.ListenTarget(chromeCtx, func(ev any) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			c := chromedp.FromContext(chromeCtx)
			if c == nil || c.Target == nil {
				return
			}
			ectx := cdpbase.WithExecutor(chromeCtx, c.Target)
			_ = fetch.FulfillRequest(paused.RequestID, 200).
				WithBody(stubBody).
				WithResponseHeaders([]*fetch.HeaderEntry{
					{Name: "Content-Type", Value: "text/html; charset=utf-8"},
				}).Do(ectx)
		}()
	})

	if err := chromedp.Run(chromeCtx, fetch.Enable()); err != nil {
		return fmt.Errorf("Fetch.enable: %w", err)
	}

	for _, origin := range origins {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := chromedp.Run(chromeCtx, chromedp.Navigate(origin)); err != nil {
			// A single unreachable/invalid origin must not abort the batch.
			continue
		}
		if err := chromedp.Run(chromeCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return visit(ctx, origin)
		})); err != nil {
			continue
		}
	}

	if err := chromedp.Cancel(chromeCtx); err != nil {
		return fmt.Errorf("graceful close (flush): %w", err)
	}
	return nil
}
