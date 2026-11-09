package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mvanhorn/agentcookie/internal/config"
	"github.com/mvanhorn/agentcookie/internal/keystore"
	"github.com/mvanhorn/agentcookie/internal/transport"
)

// A sink retired from source.yaml's sinks: keeps its keys/ file, since
// plain peers have no unpair. That key must not keep pulling the jar.
//
// Guard-revert: drop the sinkPeers check in authorizePull and this goes red.
func TestPullRefusesKeyOfSinkNotInSinksList(t *testing.T) {
	f := newAuthFixture(t, "")
	// grokbot has a key file but is neither a listed sink nor a teammate.
	if rec := f.pull(t, grokKey, strangerIP); rec.Code != http.StatusForbidden {
		t.Fatalf("retired sink key status = %d, want 403", rec.Code)
	}
	if rec := f.pull(t, museKey, strangerIP); rec.Code != http.StatusOK {
		t.Fatalf("listed sink key status = %d, want 200", rec.Code)
	}
}

// pullOptInFixture runs startWatchPullListener with the tailnet seam on
// loopback and no teammates, counting tailnet lookups.
type pullOptInFixture struct {
	dir     string
	lookups atomic.Int32
	addr    chan string
}

func newPullOptInFixture(t *testing.T) *pullOptInFixture {
	t.Helper()
	f := &pullOptInFixture{dir: t.TempDir(), addr: make(chan string, 4)}
	withConfigDir(t, f.dir)
	prevTeammates := activeTeammates
	activeTeammates = nil
	t.Cleanup(func() { activeTeammates = prevTeammates })
	prevListen := sourcePullListen
	sourcePullListen = ""
	t.Cleanup(func() { sourcePullListen = prevListen })
	t.Cleanup(setPullListenerSeamsForTesting(func(context.Context) (string, error) {
		f.lookups.Add(1)
		return "127.0.0.1", nil
	}, "0", 5*time.Millisecond, time.Hour))
	return f
}

func (f *pullOptInFixture) start(t *testing.T, cfg *config.SourceConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	err := startWatchPullListener(ctx, cfg, func(up bool, addr string) {
		if up {
			f.addr <- addr
		}
	})
	if err != nil {
		t.Fatalf("startWatchPullListener: %v", err)
	}
}

// A push-only source must not open 9998 at all.
//
// Guard-revert: make pullListenerWanted return true and this goes red.
func TestPullListenerOffWithoutOptIn(t *testing.T) {
	f := newPullOptInFixture(t)
	f.start(t, &config.SourceConfig{})
	select {
	case addr := <-f.addr:
		t.Fatalf("listener came up on %s without pull_listener.enabled", addr)
	case <-time.After(100 * time.Millisecond):
	}
	if n := f.lookups.Load(); n != 0 {
		t.Fatalf("listener looked up a tailnet address %d times without the opt-in", n)
	}
}

// With the opt-in, a sink listed in sinks: pulls over the real listener
// and gets the envelope sealed under its own key.
//
// Guard-revert: pass nil sink peers to newPullMux in startWatchPullListener
// and this goes red.
func TestPullListenerServesListedSinkWhenOptedIn(t *testing.T) {
	f := newPullOptInFixture(t)
	const peer, key = "muse-box", "muse-box-paired-key-32-bytes-lng"
	if err := keystore.Save(f.dir, &keystore.PeerKey{Peer: peer, Key: []byte(key)}); err != nil {
		t.Fatal(err)
	}
	prevPayload := pullPayloadCache.Load()
	pullPayloadCache.Store([]byte(testEnvJSON))
	t.Cleanup(func() { pullPayloadCache.Store(prevPayload) })

	f.start(t, &config.SourceConfig{
		Sinks:        []config.SinkTarget{{URL: "http://muse-box:9999/sync", Peer: peer}},
		PullListener: config.PullListenerRef{Enabled: true},
	})
	var addr string
	select {
	case addr = <-f.addr:
	case <-time.After(5 * time.Second):
		t.Fatal("listener never came up with pull_listener.enabled")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatalf("listener addr %q: %v", addr, err)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/pull", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.SignRequest(req, key, time.Now()); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listed sink /pull status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := transport.OpenWithSecret(body, key)
	if err != nil || string(got) != testEnvJSON {
		t.Fatalf("envelope did not open under the sink's key: %v", err)
	}
}
