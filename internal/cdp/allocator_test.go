package cdp

import (
	"strings"
	"testing"
)

// TestInjectorFlagsNoKeyringPoisoning is the regression guard for the
// 2026-08 sink bug: a Chrome launched with --use-mock-keychain (or
// --password-store=basic on Linux) seals cookie values under a key the
// user's real Chrome does not hold, so the first normal launch of the
// profile purges every synced row. chromedp ships both switches in
// DefaultExecAllocatorOptions; the injector must not inherit them.
func TestInjectorFlagsNoKeyringPoisoning(t *testing.T) {
	for _, f := range injectorFlags {
		for _, banned := range keyringPoisoningFlags {
			if f.name == banned {
				t.Errorf("injectorFlags carries --%s=%v; it changes Chrome's os_crypt key and makes the seeded profile unreadable to the user's real Chrome", f.name, f.value)
			}
		}
	}
}

// TestInjectorAllocatorOptionsRendersEveryFlag keeps the option builder
// honest about the spec it is derived from.
func TestInjectorAllocatorOptionsRendersEveryFlag(t *testing.T) {
	if got, want := len(injectorAllocatorOptions()), len(injectorFlags); got != want {
		t.Errorf("rendered %d allocator options, want %d", got, want)
	}
}

// TestInjectorFlagsHeadlessAndFirstRun pins the switches the injector
// genuinely needs: a launchd sink has no GUI session to dismiss a
// first-run dialog, and the wizard may have just created the profile.
func TestInjectorFlagsHeadlessAndFirstRun(t *testing.T) {
	required := []string{"headless", "no-first-run", "no-default-browser-check"}
	present := map[string]bool{}
	for _, f := range injectorFlags {
		present[f.name] = true
	}
	for _, r := range required {
		if !present[r] {
			t.Errorf("injectorFlags is missing --%s", r)
		}
	}
}

// TestInjectorFlagsNoDuplicates catches a copy-paste slip when this list
// is resynced against a newer chromedp.
func TestInjectorFlagsNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range injectorFlags {
		if seen[f.name] {
			t.Errorf("duplicate flag --%s in injectorFlags", f.name)
		}
		seen[f.name] = true
		if strings.HasPrefix(f.name, "-") {
			t.Errorf("flag %q must be a bare name, not include leading dashes", f.name)
		}
	}
}
