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
// the targets, waits minAge, lists again, and picks page targets that were
// about:blank both times. Each pick is checked again against a fresh listing
// right before it is closed. It never closes a page with a real URL, a
// non-page target (service worker, extension, browser UI), a tab outside the
// default browser context (an agent's own context, incognito), or the last
// page, which on Linux would quit the browser. It returns the number of tabs
// closed.
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

	bctx, err := browserExecutor(browserCtx)
	if err != nil {
		return 0, err
	}
	// The default context is identified by id, not by absence from the
	// explicit list: incognito contexts are in neither.
	_, defaultID, err := target.GetBrowserContexts().Do(bctx)
	if err != nil {
		return 0, fmt.Errorf("livecdp: sweep: list browser contexts: %w", err)
	}
	if defaultID == "" {
		// This Chrome does not report its default context, so no blank
		// tab can be placed safely; close nothing.
		return 0, nil
	}

	list := func() ([]*target.Info, error) { return chromedp.Targets(browserCtx) }
	closeTarget := func(id target.ID) error { return target.CloseTarget(id).Do(bctx) }
	return closeStaleBlanks(staleBlankTargets(first, second, defaultID), list, closeTarget, defaultID)
}

// staleBlankTargets picks the sweep candidates from two listings taken at
// least minAge apart: blank tabs in the default context that were already
// blank in the first listing. It returns each candidate's info from the
// second listing, so a later change can be seen.
func staleBlankTargets(first, second []*target.Info, defaultID cdp.BrowserContextID) []*target.Info {
	wasBlank := map[target.ID]bool{}
	for _, info := range first {
		if isSweepableBlank(info, defaultID) {
			wasBlank[info.TargetID] = true
		}
	}
	var stale []*target.Info
	for _, info := range second {
		if isSweepableBlank(info, defaultID) && wasBlank[info.TargetID] {
			stale = append(stale, info)
		}
	}
	return stale
}

// closeStaleBlanks closes the candidates one at a time. Before each close it
// lists the targets again and skips a candidate that is gone, is no longer a
// blank default-context tab, or has been attached to since it was sampled.
// It stops once only one page would remain.
func closeStaleBlanks(candidates []*target.Info, list func() ([]*target.Info, error), closeTarget func(target.ID) error, defaultID cdp.BrowserContextID) (int, error) {
	closed := 0
	var firstErr error
	for _, sampled := range candidates {
		live, err := list()
		if err != nil {
			return closed, fmt.Errorf("livecdp: sweep: list targets: %w", err)
		}
		if livePages(live) <= 1 {
			break
		}
		cur := findTarget(live, sampled.TargetID)
		if !isSweepableBlank(cur, defaultID) || (cur.Attached && !sampled.Attached) {
			continue
		}
		if err := closeTarget(cur.TargetID); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("livecdp: sweep: close target %s: %w", cur.TargetID, err)
			}
			continue
		}
		closed++
	}
	return closed, firstErr
}

// livePages counts the page targets a user could see; a prerender target is
// not one, so it does not keep the browser's last window open.
func livePages(infos []*target.Info) int {
	n := 0
	for _, info := range infos {
		if info != nil && info.Type == "page" && info.Subtype != "prerender" {
			n++
		}
	}
	return n
}

func findTarget(infos []*target.Info, id target.ID) *target.Info {
	for _, info := range infos {
		if info != nil && info.TargetID == id {
			return info
		}
	}
	return nil
}

// isSweepableBlank reports whether info is a plain about:blank tab in the
// default browser context.
func isSweepableBlank(info *target.Info, defaultID cdp.BrowserContextID) bool {
	return info != nil && info.Type == "page" && info.Subtype == "" &&
		info.URL == "about:blank" && info.BrowserContextID == defaultID
}
