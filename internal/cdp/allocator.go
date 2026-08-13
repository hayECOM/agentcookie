package cdp

import "github.com/chromedp/chromedp"

// injectorFlag is one Chrome command-line switch for the one-shot
// injector Chrome.
type injectorFlag struct {
	name  string
	value any
}

// osCryptFlags are the switches that decide WHICH KEY Chrome's os_crypt
// uses to seal cookie values on macOS/Linux. They are the single most
// load-bearing thing on this path, and they are pinned here on purpose.
//
// The seeded profile is only ever read back by an automation launcher --
// agent-browser, Playwright, Puppeteer, chromedp -- and every one of
// those launches Chrome with --use-mock-keychain (and
// --password-store=basic), which makes Chrome derive its AES key from
// the literal string "mock_password" instead of touching the OS keyring.
// The injector MUST seal under the same key the reader will unseal with,
// or Chrome drops every row it cannot decrypt the first time the profile
// is opened -- silently, and the profile looks perfectly seeded on disk
// right up until that moment.
//
// Measured on this machine (Chrome 151 injector, agent-browser's Google
// Chrome for Testing 150 reader, 2026-08-13):
//
//	injector --use-mock-keychain  -> 5/5 seeded rows survive the read
//	injector on the real keyring  -> 0/5 survive; whole DB is purged
//
// So this is NOT "the automation default is wrong, override it". It is
// "the automation default is required, pin it". Today these values are
// inherited by accident from chromedp.DefaultExecAllocatorOptions; listed
// explicitly, a chromedp upgrade can neither remove them nor change them
// without TestInjectorFlagsPinOSCrypt going red.
//
// If a deployment ever reads this profile with a NORMALLY launched
// Google Chrome (real "Chrome Safe Storage" Keychain item), these two
// flags are exactly what has to change -- and everything else here can
// stay put.
var osCryptFlags = []injectorFlag{
	{"use-mock-keychain", true},
	{"password-store", "basic"},
}

// injectorFlags mirrors chromedp.DefaultExecAllocatorOptions (itself
// modeled on Puppeteer's defaults) plus the headless/first-run switches
// the injector needs. Kept as an explicit list rather than
// chromedp.DefaultExecAllocatorOptions so the os_crypt flags above are
// visible and pinned; keep the rest in sync with chromedp on upgrade.
var injectorFlags = append([]injectorFlag{
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
}, osCryptFlags...)

// injectorAllocatorOptions renders injectorFlags as chromedp allocator
// options. Callers append UserDataDir themselves.
func injectorAllocatorOptions() []chromedp.ExecAllocatorOption {
	opts := make([]chromedp.ExecAllocatorOption, 0, len(injectorFlags)+1)
	for _, f := range injectorFlags {
		opts = append(opts, chromedp.Flag(f.name, f.value))
	}
	return opts
}
