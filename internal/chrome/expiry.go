package chrome

import "time"

// IsExpiredAt reports whether c carries a real expiry at or before now.
// Session cookies (ExpiresUTC == 0) never count as expired.
//
// Push paths must skip these. Network.setCookies, Storage.setCookies and
// WebKit's cookie store all treat a cookie set with a past expiry as a
// DELETE, so re-pushing a source's stale row wipes the fresh cookie the
// destination browser minted for itself. Measured 2026-10-25: the laptop's
// expired Apple shield cookies (shld_bt_*), re-pushed every sync, deleted
// the human Chrome's live ones and Add to Bag answered HTTP 541.
func IsExpiredAt(c Cookie, now time.Time) bool {
	if c.ExpiresUTC == 0 {
		return false
	}
	return !chromeTimeToUnix(c.ExpiresUTC).After(now)
}

// DropExpired returns the cookies that are not expired at now, and how many
// were dropped. The input slice is not modified.
func DropExpired(cookies []Cookie, now time.Time) ([]Cookie, int) {
	kept := make([]Cookie, 0, len(cookies))
	for _, c := range cookies {
		if !IsExpiredAt(c, now) {
			kept = append(kept, c)
		}
	}
	return kept, len(cookies) - len(kept)
}
