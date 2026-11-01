package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mvanhorn/agentcookie/internal/config"
	"github.com/mvanhorn/agentcookie/internal/livecdp"
)

// liveCDPSweep closes stale about:blank tabs in the running Chrome at a
// CDP endpoint. Tests replace it so they never dial Chrome.
var liveCDPSweep = livecdp.SweepStaleBlankTabs

// startLiveCDPSweep closes, in the background, the about:blank tabs earlier
// sink versions leaked into the live CDP Chrome (one per inject), so the
// pile clears on the next sink start. It runs when live CDP is on and the
// sink may modify Chrome (not --dry-run). It reports whether it started.
func startLiveCDPSweep(ctx context.Context, cfg *config.SinkConfig) bool {
	if !cfg.LiveCDP.Enabled || sinkDryRun {
		return false
	}
	endpoint := cfg.LiveCDP.Endpoint
	if endpoint == "" {
		endpoint = livecdp.DefaultCDPEndpoint
	}
	go func() {
		sweepCtx, cancel := context.WithTimeout(ctx, livecdp.DefaultSweepMinAge+time.Minute)
		defer cancel()
		closed, err := liveCDPSweep(sweepCtx, endpoint, livecdp.DefaultSweepMinAge)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "agentcookie sink: stale-tab sweep at %s: closed %d, then failed: %v\n", endpoint, closed, err)
		case closed > 0:
			fmt.Fprintf(os.Stderr, "agentcookie sink: stale-tab sweep closed %d about:blank tab(s) at %s\n", closed, endpoint)
		}
	}()
	return true
}
