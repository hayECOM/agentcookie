package livecdp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/mvanhorn/agentcookie/internal/chrome"
)

func TestStaleBlankTargets(t *testing.T) {
	page := func(id, url string) *target.Info {
		return &target.Info{TargetID: target.ID(id), Type: "page", URL: url}
	}
	stale1 := page("stale1", "about:blank")
	stale2 := page("stale2", "about:blank")
	real := page("real", "https://github.com/")
	navigated := page("navigated", "https://example.com/")
	fresh := page("fresh", "about:blank")
	agent := &target.Info{TargetID: "agent", Type: "page", URL: "about:blank", BrowserContextID: "agentctx"}
	prerender := &target.Info{TargetID: "pre", Type: "page", Subtype: "prerender", URL: "about:blank"}
	worker := &target.Info{TargetID: "sw", Type: "service_worker", URL: "about:blank"}
	ui := page("ui", "chrome://newtab-footer/")

	first := []*target.Info{stale1, stale2, real, page("navigated", "about:blank"), agent, prerender, worker, ui, nil}
	second := []*target.Info{stale1, stale2, real, navigated, fresh, agent, prerender, worker, ui, nil}
	explicit := map[cdp.BrowserContextID]bool{"agentctx": true}

	got := staleBlankTargets(first, second, explicit)
	want := []target.ID{"stale1", "stale2"}
	if !slices.Equal(got, want) {
		t.Fatalf("staleBlankTargets = %v, want %v", got, want)
	}
}

func TestStaleBlankTargets_KeepsLastPage(t *testing.T) {
	a := &target.Info{TargetID: "a", Type: "page", URL: "about:blank"}
	b := &target.Info{TargetID: "b", Type: "page", URL: "about:blank"}
	listing := []*target.Info{a, b}
	if got := staleBlankTargets(listing, listing, nil); len(got) != 1 {
		t.Fatalf("all-blank browser: closing %v, want exactly 1 of 2 so a page stays open", got)
	}
	if got := staleBlankTargets([]*target.Info{a}, []*target.Info{a}, nil); len(got) != 0 {
		t.Fatalf("single blank page: closing %v, want none", got)
	}
}

// launchSweepChrome starts a headless Chrome on its own loopback port for
// the live tests (never the shared 9223 browser) and returns its endpoint
// and a browser-level chromedp context on it.
func launchSweepChrome(t *testing.T, port int) (string, context.Context) {
	t.Helper()
	if os.Getenv("AGENTCOOKIE_LIVE_CDP_TEST") == "" {
		t.Skip("set AGENTCOOKIE_LIVE_CDP_TEST=1 to run live-Chrome tab tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	oc, err := LaunchOwnedChrome(ctx, "", t.TempDir(), port, true)
	if err != nil {
		t.Fatalf("LaunchOwnedChrome: %v", err)
	}
	t.Cleanup(func() { _ = oc.Close() })
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, oc.Endpoint)
	t.Cleanup(allocCancel)
	bctx, bcancel := chromedp.NewContext(allocCtx)
	t.Cleanup(bcancel)
	return oc.Endpoint, bctx
}

func pageTargets(t *testing.T, bctx context.Context) map[target.ID]string {
	t.Helper()
	infos, err := chromedp.Targets(bctx)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	pages := map[target.ID]string{}
	for _, info := range infos {
		if info.Type == "page" {
			pages[info.TargetID] = info.URL
		}
	}
	return pages
}

func createTab(t *testing.T, bctx context.Context, url string) target.ID {
	t.Helper()
	ex, err := browserExecutor(bctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := target.CreateTarget(url).Do(ex)
	if err != nil {
		t.Fatalf("create tab %s: %v", url, err)
	}
	return id
}

// TestAttachAndInject_LeavesNoTabs is the regression test for the sink
// leaking one about:blank tab per inject: repeated inject cycles against a
// debug-port Chrome create no page target at all and leave the page
// targets unchanged. Counting creations, not just survivors, matters: the
// old path created a tab per inject and closed it on cancel with a 1s
// budget, which a quiet test browser meets and a loaded real one did not.
func TestAttachAndInject_LeavesNoTabs(t *testing.T) {
	endpoint, bctx := launchSweepChrome(t, 9412)
	before := pageTargets(t, bctx)

	var mu sync.Mutex
	var created []string
	chromedp.ListenBrowser(bctx, func(ev any) {
		if e, ok := ev.(*target.EventTargetCreated); ok && e.TargetInfo.Type == "page" {
			mu.Lock()
			created = append(created, e.TargetInfo.URL)
			mu.Unlock()
		}
	})
	ex, err := browserExecutor(bctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.SetDiscoverTargets(true).Do(ex); err != nil {
		t.Fatalf("Target.setDiscoverTargets: %v", err)
	}
	// setDiscoverTargets replays existing targets as created; drop those.
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	created = nil
	mu.Unlock()

	cookie := chrome.Cookie{HostKey: "example.com", Name: "ac_tabs", Value: "v", Path: "/", IsSecure: 1, SameSite: 1}
	for i := range 5 {
		start := time.Now()
		n, err := AttachAndInject(context.Background(), endpoint, []chrome.Cookie{cookie})
		if err != nil {
			t.Fatalf("inject cycle %d: %v", i, err)
		}
		if n < 1 {
			t.Fatalf("inject cycle %d: injected %d contexts, want >=1", i, n)
		}
		t.Logf("inject cycle %d: %s", i, time.Since(start))
	}
	// A leaked tab would be closing or open by now; give stragglers a beat.
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(created) != 0 {
		t.Errorf("5 injects created %d page target(s): %v", len(created), created)
	}
	if after := pageTargets(t, bctx); len(after) != len(before) {
		t.Fatalf("page targets before %d (%v), after 5 injects %d (%v)", len(before), before, len(after), after)
	}
}

func TestSweepStaleBlankTabs_Live(t *testing.T) {
	endpoint, bctx := launchSweepChrome(t, 9413)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>real</body></html>"))
	}))
	t.Cleanup(srv.Close)

	stale := []target.ID{createTab(t, bctx, "about:blank"), createTab(t, bctx, "about:blank"), createTab(t, bctx, "about:blank")}
	real := createTab(t, bctx, srv.URL)

	const minAge = 3 * time.Second
	type result struct {
		closed int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		n, err := SweepStaleBlankTabs(context.Background(), endpoint, minAge)
		done <- result{n, err}
	}()
	// Opened after the sweep's first listing, so younger than minAge.
	time.Sleep(minAge / 2)
	fresh := createTab(t, bctx, "about:blank")

	res := <-done
	if res.err != nil {
		t.Fatalf("SweepStaleBlankTabs: %v", res.err)
	}
	time.Sleep(500 * time.Millisecond)
	pages := pageTargets(t, bctx)
	for _, id := range stale {
		if _, ok := pages[id]; ok {
			t.Errorf("stale about:blank tab %s survived the sweep", id)
		}
	}
	if _, ok := pages[real]; !ok {
		t.Errorf("real-URL tab %s was closed", real)
	}
	if _, ok := pages[fresh]; !ok {
		t.Errorf("fresh about:blank tab %s was closed", fresh)
	}
	// The launch tab (about:blank) is stale too, unless it was kept as the
	// last page; either way at least the three seeded tabs went.
	if res.closed < len(stale) {
		t.Errorf("closed %d tabs, want >= %d", res.closed, len(stale))
	}
}
