package cli

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestMain keeps every test in this package off the real Chrome quit. A
// non-skip sink config sends /sync through applyEnvelopeToSink, whose
// chromectl.WithChromeDown quits every Google Chrome on the machine; on
// 2026-11-09 TestSinkUniversalMode_StillUnionsExtraProfiles did exactly
// that to the human Chrome lane. The stub runs the write with Chrome
// treated as already down.
func TestMain(m *testing.M) {
	chromeDown = func(_ context.Context, _ time.Duration, fn func() error) error { return fn() }
	os.Exit(m.Run())
}
