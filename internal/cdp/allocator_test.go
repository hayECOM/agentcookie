package cdp

import (
	"strings"
	"testing"
)

// TestInjectorFlagsPinOSCrypt is the regression guard for the key half
// of the 2026-08 sink bug. The injector must seal cookie values under
// the same os_crypt key the reader unseals with. Every automation
// launcher that reads this profile (agent-browser, Playwright,
// Puppeteer, chromedp) runs Chrome with --use-mock-keychain, so the
// injector has to as well; sealing under the real OS keyring instead
// purges the entire cookie DB on the reader's first launch.
func TestInjectorFlagsPinOSCrypt(t *testing.T) {
	want := map[string]any{"use-mock-keychain": true, "password-store": "basic"}
	got := map[string]any{}
	for _, f := range injectorFlags {
		if _, ok := want[f.name]; ok {
			got[f.name] = f.value
		}
	}
	for name, wantVal := range want {
		gotVal, ok := got[name]
		if !ok {
			t.Errorf("injectorFlags is missing --%s; without it the reader's Chrome cannot decrypt what we seal and drops every synced row", name)
			continue
		}
		if gotVal != wantVal {
			t.Errorf("--%s = %v, want %v", name, gotVal, wantVal)
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
// is resynced against a newer chromedp. A duplicate is not harmless
// here: map iteration order would decide which value wins.
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
