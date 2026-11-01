package livecdp

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// DefaultSweepMinAge is how long an about:blank tab must stay blank before
// SweepStaleBlankTabs closes it. The attached Chrome is shared: another
// client may have just opened a blank tab it is about to navigate.
const DefaultSweepMinAge = 60 * time.Second

// SweepStaleBlankTabs closes the about:blank tabs left in the Chrome at
// endpoint by earlier attach-and-inject runs, which leaked one tab per
// inject. CDP reports no target creation time, so age is observed: it lists
// the targets, waits minAge, lists again, and closes only page targets that
// were about:blank both times. It never closes a page with a real URL, a
// non-page target (service worker, extension, browser UI), a tab in an
// explicit browser context (an agent's own context), or the last page
// target, which on Linux would quit the browser. It returns the number of
// tabs closed.
func SweepStaleBlankTabs(ctx context.Context, endpoint string, minAge time.Duration) (int, error) {
	if endpoint == "" {
		endpoint = DefaultCDPEndpoint
	}
	if err := waitForCDPEndpoint(ctx, endpoint, 5*time.Second); err != nil {
		return 0, fmt.Errorf("livecdp: sweep: cannot reach CDP endpoint %s: %w", endpoint, err)
	}
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, endpoint+"/json/version")
	defer allocCancel()
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	first, err := chromedp.Targets(browserCtx)
	if err != nil {
		return 0, fmt.Errorf("livecdp: sweep: list targets: %w", err)
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(minAge):
	}
	second, err := chromedp.Targets(browserCtx)
	if err != nil {
		return 0, fmt.Errorf("livecdp: sweep: list targets: %w", err)
	}
	explicit, err := explicitContextSet(browserCtx)
	if err != nil {
		// Without the set, a blank tab in an agent's own context cannot
		// be told apart, so close nothing.
		return 0, fmt.Errorf("livecdp: sweep: list browser contexts: %w", err)
	}

	bctx, err := browserExecutor(browserCtx)
	if err != nil {
		return 0, err
	}
	closed := 0
	var firstErr error
	for _, id := range staleBlankTargets(first, second, explicit) {
		if err := target.CloseTarget(id).Do(bctx); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("livecdp: sweep: close target %s: %w", id, err)
			}
			continue
		}
		closed++
	}
	return closed, firstErr
}

// staleBlankTargets picks the targets SweepStaleBlankTabs closes from two
// listings taken at least minAge apart: blank tabs in the default context
// that were already blank in the first listing. It keeps at least one page
// target open.
func staleBlankTargets(first, second []*target.Info, explicit map[cdp.BrowserContextID]bool) []target.ID {
	wasBlank := map[target.ID]bool{}
	for _, info := range first {
		if isSweepableBlank(info, explicit) {
			wasBlank[info.TargetID] = true
		}
	}
	pages := 0
	var stale []target.ID
	for _, info := range second {
		if info == nil || info.Type != "page" {
			continue
		}
		pages++
		if isSweepableBlank(info, explicit) && wasBlank[info.TargetID] {
			stale = append(stale, info.TargetID)
		}
	}
	if len(stale) > 0 && len(stale) == pages {
		stale = stale[1:]
	}
	return stale
}

// isSweepableBlank reports whether info is a plain about:blank tab in the
// default browser context.
func isSweepableBlank(info *target.Info, explicit map[cdp.BrowserContextID]bool) bool {
	return info != nil && info.Type == "page" && info.Subtype == "" &&
		info.URL == "about:blank" && !explicit[info.BrowserContextID]
}
