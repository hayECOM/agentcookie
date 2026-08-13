package cdp

import "github.com/chromedp/chromedp"

// injectorFlag is one Chrome command-line switch for the one-shot
// injector Chrome.
type injectorFlag struct {
	name  string
	value any
}

// keyringPoisoningFlags are the switches that change which key Chrome's
// os_crypt uses to seal cookie values. They must never appear on the
// injector's command line.
//
// This is the 2026-08 sink bug. chromedp.DefaultExecAllocatorOptions
// ships --use-mock-keychain (and --password-store=basic) because a
// throwaway automation Chrome has no reason to touch the real OS
// keyring. Our injector is the opposite case: it seeds a profile that a
// normally-launched Chrome opens later. Under --use-mock-keychain,
// macOS Chrome derives its AES key from the literal string
// "mock_password" instead of the login Keychain's "Chrome Safe Storage"
// item. Every row lands sealed under a key the real Chrome does not
// hold, so the first real launch fails to decrypt them and PURGES them
// -- the profile looks correctly seeded on disk right up until the
// moment it is used. (--password-store=basic is the same failure on
// Linux: hardcoded "peanuts" instead of the desktop keyring.)
//
// injectorFlags is therefore an explicit list rather than
// chromedp.DefaultExecAllocatorOptions plus overrides: a chromedp
// upgrade must not be able to reintroduce a keyring flag silently, and
// chromedp.Flag cannot un-set a string flag like --password-store once
// the defaults have set it.
var keyringPoisoningFlags = []string{"use-mock-keychain", "password-store"}

// injectorFlags mirrors chromedp.DefaultExecAllocatorOptions (itself
// modeled on Puppeteer's defaults) minus keyringPoisoningFlags, plus the
// headless/first-run switches the injector needs. Keep it in sync with
// chromedp on upgrade; TestInjectorFlagsNoKeyringPoisoning guards the
// part that matters.
var injectorFlags = []injectorFlag{
	// Headless, and no first-run/welcome UI: the wizard may have just
	// created this profile dir, and a launchd sink has no one to
	// dismiss a dialog.
	{"headless", true},
	{"hide-scrollbars", true},
	{"mute-audio", true},
	{"no-first-run", true},
	{"no-default-browser-check", true},
	{"disable-gpu", true},

	// After Puppeteer's default behavior.
	{"disable-background-networking", true},
	{"enable-features", "NetworkService,NetworkServiceInProcess"},
	{"disable-background-timer-throttling", true},
	{"disable-backgrounding-occluded-windows", true},
	{"disable-breakpad", true},
	{"disable-client-side-phishing-detection", true},
	{"disable-default-apps", true},
	{"disable-dev-shm-usage", true},
	{"disable-extensions", true},
	{"disable-features", "site-per-process,Translate,BlinkGenPropertyTrees"},
	{"disable-hang-monitor", true},
	{"disable-ipc-flooding-protection", true},
	{"disable-popup-blocking", true},
	{"disable-prompt-on-repost", true},
	{"disable-renderer-backgrounding", true},
	{"disable-sync", true},
	{"force-color-profile", "srgb"},
	{"metrics-recording-only", true},
	{"safebrowsing-disable-auto-update", true},
	{"enable-automation", true},
}

// injectorAllocatorOptions renders injectorFlags as chromedp allocator
// options. Callers append UserDataDir themselves.
func injectorAllocatorOptions() []chromedp.ExecAllocatorOption {
	opts := make([]chromedp.ExecAllocatorOption, 0, len(injectorFlags)+1)
	for _, f := range injectorFlags {
		opts = append(opts, chromedp.Flag(f.name, f.value))
	}
	return opts
}
