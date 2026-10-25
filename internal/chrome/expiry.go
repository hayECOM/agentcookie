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

// PushExpiryMargin is how close to its expiry a cookie may be and still be
// pushed. A push is not instant -- the cold CDP path spends up to 15s
// starting Chrome, cmux sets a big jar in batches -- so a cookie that passes
// an exact check can arrive already expired and act as a delete anyway.
// Skipping a cookie with under two minutes left costs nothing: the
// destination keeps whatever it has.
const PushExpiryMargin = 2 * time.Minute

// DropExpiredForPush is DropExpired with PushExpiryMargin applied: the set a
// push path may hand to a browser or a cookie store.
func DropExpiredForPush(cookies []Cookie, now time.Time) ([]Cookie, int) {
	return DropExpired(cookies, now.Add(PushExpiryMargin))
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
