package chrome

import (
	"testing"
	"time"
)

func TestIsExpiredAt(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		expires int64
		want    bool
	}{
		{"session cookie never expires", 0, false},
		{"past expiry", chromeMicros(now.Add(-time.Hour)), true},
		{"expiry exactly now", chromeMicros(now), true},
		{"future expiry", chromeMicros(now.Add(time.Hour)), false},
	}
	for _, tc := range cases {
		if got := IsExpiredAt(Cookie{ExpiresUTC: tc.expires}, now); got != tc.want {
			t.Errorf("%s: IsExpiredAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDropExpired_KeepsSessionAndLiveCookies(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	in := []Cookie{
		{Name: "shld_bt_ck", ExpiresUTC: chromeMicros(now.Add(-36 * time.Hour))},
		{Name: "dssid2"},
		{Name: "as_atb", ExpiresUTC: chromeMicros(now.Add(24 * time.Hour))},
	}
	kept, dropped := DropExpired(in, now)
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if len(kept) != 2 || kept[0].Name != "dssid2" || kept[1].Name != "as_atb" {
		t.Errorf("kept = %+v, want [dssid2 as_atb]", kept)
	}
	if in[0].Name != "shld_bt_ck" {
		t.Error("DropExpired modified its input")
	}
}
