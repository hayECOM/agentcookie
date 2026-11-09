package cli

import (
	"strings"
	"testing"

	"github.com/mvanhorn/agentcookie/internal/config"
	"github.com/mvanhorn/agentcookie/internal/protocol"
)

// TestLeveldbSummary_SkipPathNeverReportsABareZero is the guard for the
// line that cost a diagnosis pass: on the skip_chrome_sqlite path no
// leveldb is written at all, so "+ 0 localStorage origins" describes a
// surface that was never touched and reads as "the source sent none".
func TestLeveldbSummary_SkipPathNeverReportsABareZero(t *testing.T) {
	skip := &config.SinkConfig{SkipChromeSQLite: true}
	skipWithInstall := &config.SinkConfig{
		SkipChromeSQLite: true,
		CDP:              config.CDPRef{Enabled: true, ProfileDir: "~/p", LocalStorage: true},
	}
	real := &config.SinkConfig{}

	cases := []struct {
		name        string
		cfg         *config.SinkConfig
		env         *protocol.SyncEnvelope
		result      writeResult
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "skip path with a payload says it was discarded",
			cfg:         skip,
			env:         &protocol.SyncEnvelope{LocalStorageTarball: []byte("xxxx")},
			wantContain: "discarded",
			wantAbsent:  "0 localStorage origins",
		},
		{
			name:        "skip path with no payload says nothing to discard",
			cfg:         skip,
			env:         &protocol.SyncEnvelope{},
			wantContain: "nothing to discard",
			wantAbsent:  "0 localStorage origins",
		},
		{
			name:        "skip path with the installer on says where it went",
			cfg:         skipWithInstall,
			env:         &protocol.SyncEnvelope{LocalStorageTarball: []byte("xxxx")},
			wantContain: "ls -> CDP profile",
			wantAbsent:  "0 localStorage origins",
		},
		{
			name:        "real chrome path keeps the genuine counts",
			cfg:         real,
			env:         &protocol.SyncEnvelope{LocalStorageTarball: []byte("xxxx")},
			result:      writeResult{LocalStorage: 12, IndexedDB: 3},
			wantContain: "12 localStorage origins + 3 indexedDB origins",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := leveldbSummary(tc.cfg, tc.env, tc.result)
			if !strings.Contains(got, tc.wantContain) {
				t.Errorf("leveldbSummary = %q, want it to contain %q", got, tc.wantContain)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("leveldbSummary = %q, must not contain %q", got, tc.wantAbsent)
			}
		})
	}
}

// TestTarballDisposition_DoesNotClaimADiscardThatDidNotHappen: saying
// "DISCARDED" when nothing arrived is the same misdiagnosis in the other
// direction -- it tells an operator the source is sending when it is not.
func TestTarballDisposition_DoesNotClaimADiscardThatDidNotHappen(t *testing.T) {
	skip := &config.SinkConfig{SkipChromeSQLite: true}

	got := tarballDisposition(skip, &protocol.SyncEnvelope{})
	if strings.Contains(got, "DISCARDED") {
		t.Errorf("empty envelope reported as DISCARDED: %q", got)
	}
	if !strings.Contains(got, "nothing to discard") {
		t.Errorf("empty envelope disposition = %q, want it to say nothing to discard", got)
	}

	got = tarballDisposition(skip, &protocol.SyncEnvelope{LocalStorageTarball: []byte("abc")})
	if !strings.Contains(got, "DISCARDED") {
		t.Errorf("a real payload that is dropped must say DISCARDED, got %q", got)
	}

	withInstall := &config.SinkConfig{
		SkipChromeSQLite: true,
		CDP:              config.CDPRef{Enabled: true, LocalStorage: true},
	}
	got = tarballDisposition(withInstall, &protocol.SyncEnvelope{LocalStorageTarball: []byte("abc")})
	if strings.Contains(got, "DISCARDED") {
		t.Errorf("an installed payload must not be reported as discarded: %q", got)
	}
	if !strings.Contains(got, "CDP profile") {
		t.Errorf("disposition = %q, want it to name the CDP profile", got)
	}

	// IndexedDB still has nowhere to go even when localStorage installs.
	got = tarballDisposition(withInstall, &protocol.SyncEnvelope{
		LocalStorageTarball: []byte("abc"),
		IndexedDBTarball:    []byte("def"),
	})
	if !strings.Contains(got, "indexedDB DISCARDED") {
		t.Errorf("disposition = %q, want the indexedDB discard named", got)
	}
}

// TestTarballNote_SilentWhenNothingRode covers the source-side half: a
// cdp_source-mode source carries cookies only by design, so annotating
// each of its pushes with "+0 bytes" would be noise, not news.
func TestTarballNote_SilentWhenNothingRode(t *testing.T) {
	if got := tarballNote(nil, nil); got != "" {
		t.Errorf("tarballNote(nil, nil) = %q, want empty", got)
	}
	if got := tarballNote([]byte("abc"), nil); !strings.Contains(got, "3 bytes localStorage") {
		t.Errorf("tarballNote = %q, want the localStorage size", got)
	}
	if got := tarballNote(nil, []byte("abcd")); !strings.Contains(got, "4 bytes indexedDB") {
		t.Errorf("tarballNote = %q, want the indexedDB size", got)
	}
	got := tarballNote([]byte("ab"), []byte("cde"))
	if !strings.Contains(got, "2 bytes localStorage") || !strings.Contains(got, "3 bytes indexedDB") {
		t.Errorf("tarballNote = %q, want both sizes", got)
	}
}

func TestExpandTildePath(t *testing.T) {
	t.Setenv("HOME", "/tmp/fake-home")
	cases := map[string]string{
		"~/.agentcookie/chrome-profile": "/tmp/fake-home/.agentcookie/chrome-profile",
		"~":                             "/tmp/fake-home",
		"/absolute/path":                "/absolute/path",
		"relative/path":                 "relative/path",
		"~notatilde/path":               "~notatilde/path",
	}
	for in, want := range cases {
		if got := expandTildePath(in); got != want {
			t.Errorf("expandTildePath(%q) = %q, want %q", in, got, want)
		}
	}
}
