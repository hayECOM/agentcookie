package chromectl

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestIsRunning_Live probes the real pgrep. It runs only under
// AGENTCOOKIE_TEST_REAL_CHROME=1; pgrep is harmless, but the guard keeps
// every real tool behind the same opt-in.
func TestIsRunning_Live(t *testing.T) {
	if os.Getenv(LiveChromeEnv) != "1" {
		t.Skipf("set %s=1 to probe the real Chrome", LiveChromeEnv)
	}
	if _, err := IsRunning(); err != nil {
		t.Logf("IsRunning errored (OK in CI without macOS): %v", err)
	}
}

// TestQuitAndWait_NoOpWhenNotRunning verifies that quitting when Chrome
// is not running returns nil without an AppleScript or a signal.
func TestQuitAndWait_NoOpWhenNotRunning(t *testing.T) {
	f := stubChrome(t, osaSucceeds)
	f.setRunning(false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := QuitAndWait(ctx, 500*time.Millisecond); err != nil {
		t.Errorf("expected nil when Chrome not running, got %v", err)
	}
	if calls, sigs := f.snapshot(); calls != 0 || len(sigs) != 0 {
		t.Errorf("osascript calls = %d, signals = %v; want none", calls, sigs)
	}
}

// TestQuitAndWait_RespectsContext verifies that a canceled context
// surfaces before Chrome is touched.
func TestQuitAndWait_RespectsContext(t *testing.T) {
	f := stubChrome(t, osaSucceeds)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := QuitAndWait(ctx, 5*time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("QuitAndWait err = %v, want context.Canceled", err)
	}
	if calls, sigs := f.snapshot(); calls != 0 || len(sigs) != 0 {
		t.Errorf("osascript calls = %d, signals = %v; want none", calls, sigs)
	}
}

// TestLaunchAndWait_NoOpWhenRunning verifies that launching when Chrome
// is already up returns nil without a launch.
func TestLaunchAndWait_NoOpWhenRunning(t *testing.T) {
	f := stubChrome(t, osaSucceeds)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := LaunchAndWait(ctx, 500*time.Millisecond); err != nil {
		t.Errorf("expected nil when Chrome already running, got %v", err)
	}
	if f.launches != 0 {
		t.Errorf("launches = %d, want 0", f.launches)
	}
}

// TestWithChromeDown_RunsFnAndDoesNotRelaunch verifies WithChromeDown
// quits Chrome, runs fn and, crucially, never relaunches it.
func TestWithChromeDown_RunsFnAndDoesNotRelaunch(t *testing.T) {
	f := stubChrome(t, osaUserCanceled)
	called := false
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := WithChromeDown(ctx, 500*time.Millisecond, func() error {
		called = true
		return nil
	}); err != nil {
		t.Errorf("WithChromeDown returned error: %v", err)
	}
	if !called {
		t.Error("fn was not invoked")
	}
	if running, _ := IsRunning(); running || f.launches != 0 {
		t.Errorf("running = %t, launches = %d; WithChromeDown must leave Chrome quit", running, f.launches)
	}
}

// TestWithChromeDown_PropagatesFnError ensures fn errors surface as the
// returned error and are not swallowed.
func TestWithChromeDown_PropagatesFnError(t *testing.T) {
	f := stubChrome(t, osaSucceeds)
	f.setRunning(false)
	want := errors.New("fn boom")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := WithChromeDown(ctx, 500*time.Millisecond, func() error {
		return want
	})
	if !errors.Is(got, want) {
		t.Errorf("expected fn error to propagate, got %v", got)
	}
}

// TestGuard_RealToolsPanicInTests is the regression guard for the
// 2026-11-09 incident, when a test quit the human Chrome lane. Each real
// seam must panic in a test binary without the opt-in. The calls are
// harmless if the guard is ever removed: the context is already canceled,
// so exec never starts, and pkill would get signal 0 and pgrep only reads.
// The test then fails on the missing panic instead.
func TestGuard_RealToolsPanicInTests(t *testing.T) {
	t.Setenv(LiveChromeEnv, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		tool string
		call func()
	}{
		{"pgrep", func() { _, _ = isRunning() }},
		{"osascript", func() { _, _ = runOsascript(ctx, `return 1`) }},
		{"pkill", func() { _ = sendSignal(ctx, "0") }},
		{"open", func() { _, _ = launchChrome(ctx) }},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("real %s ran in a test binary; the guard must panic", tc.tool)
				}
				if msg, _ := r.(string); !strings.Contains(msg, "real "+tc.tool) {
					t.Fatalf("panic = %v, want the chromectl guard for %s", r, tc.tool)
				}
			}()
			tc.call()
		})
	}
}

// TestGuard_LiveOptInAllowsRealTools: the opt-in is the one way past the
// guard, for a live test that means to drive the real Chrome.
func TestGuard_LiveOptInAllowsRealTools(t *testing.T) {
	t.Setenv(LiveChromeEnv, "1")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("guard panicked with the opt-in set: %v", r)
		}
	}()
	guardRealChrome("osascript")
}
