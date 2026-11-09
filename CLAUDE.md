# CLAUDE.md — agentcookie (vendored third-party, locally patched)

**This is not our code.** It is a clone of the public upstream
`github.com/mvanhorn/agentcookie` (MIT), carrying a local patch that is not
upstreamed yet. Treat upstream files as someone else's house style: match what is
already there, keep diffs small, and keep every local change on a topic branch so
it stays PR-able.

- Remotes: `origin` → `github.com/hayECOM/agentcookie` (our fork, default
  branch `prod`); `upstream` → `github.com/mvanhorn/agentcookie` (**never push
  there**). `origin/main` mirrors upstream's `main`.
- Our code lands on `prod` by PR. Upstream is merged in (never rebased) on a
  topic branch, as in the 2026-11-09 reconcile.
- Installed binary: `~/.local/bin/agentcookie` (arm64). Check what it was built
  from with `go version -m ~/.local/bin/agentcookie` (`vcs.revision`).

## What it does here

agentcookie is the laptop→mini cookie sync. The **laptop is the source** and the
**mini is the sink**. This machine is the sink only.

> **Never touch the laptop source half from here.** The source's config, keys and
> LaunchAgent live on the other machine; changing the wire format on this side
> alone breaks the sync. The sink accepts protocol versions
> `protocol.MinVersion`..`protocol.Version` (currently 1–2), so keep that window
> open when editing `internal/protocol`.

Sink layout on this machine:

| Thing | Path |
| --- | --- |
| Config (strict YAML — unknown keys are a load error) | `~/.config/agentcookie/sink.yaml` |
| Data dir | `~/.agentcookie/` |
| Plaintext mirror consumed by the injection lane | `~/.agentcookie/cookies-plain.db` |
| Seeded Chrome profile | `~/.agentcookie/chrome-profile/` |
| Replay-defense high-water marks | `~/.agentcookie/sequence.json` |
| Logs | `~/.agentcookie/logs/sink.err.log` |
| LaunchAgent | `~/Library/LaunchAgents/dev.agentcookie.sink.plist` (`dev.agentcookie.sink`) |

**Do not purge or rewrite `cookies-plain.db`** — a working manual injection lane
reads it. **Cookie values are Rony's live credentials:** never print, log, commit
or exfiltrate them. When you need to prove something about a cookie, assert on
metadata (`host_key`, name, lengths) or on the App-Bound `SHA256(host_key)`
plaintext prefix, which proves the decryption key without revealing the value.

## The chrome-profile lane, and the two invariants that keep it working

`cfg.CDP.Enabled` makes the sink spawn a one-shot headless Chrome against
`~/.agentcookie/chrome-profile` after each `/sync` and push cookies via
`Network.setCookies` (`internal/cdp`). The profile is then driven by
`agent-browser --profile ~/.agentcookie/chrome-profile`.

That lane was silently broken until 2026-08-13. Both defects were in
`internal/cdp`, both are guarded by tests now, and both are easy to reintroduce:

1. **os_crypt key must match the reader.** The reader is *not* Google Chrome — it
   is agent-browser's bundled **Google Chrome for Testing**, launched with
   `--use-mock-keychain`, which derives its AES key from the literal string
   `"mock_password"` rather than the login Keychain. The injector must seal under
   the same key. Measured: mock-key seed → 5/5 rows survive the read; real-keyring
   seed → 0/5, the whole DB is purged on first open. `internal/cdp/allocator.go`
   pins `--use-mock-keychain` and `--password-store=basic` explicitly instead of
   inheriting them by accident from `chromedp.DefaultExecAllocatorOptions`.
   Guard: `TestInjectorFlagsPinOSCrypt`.
2. **Domain cookies must keep their leading dot.** `host_key` `.airbnb.com` has to
   reach `Network.setCookies` as `Domain: ".airbnb.com"`. Chrome 151 stores
   `Domain` verbatim, and a *dotless* Domain lands as a **host-only** row no
   matter what URL accompanies it — so it never rides to `www.`. Stripping the dot
   demoted 1762 of 1883 domain-scoped cookies. Guard:
   `TestBuildCookieParam_DomainCookieKeepsLeadingDot`.
3. **Stale host-only twins must be converged away, surgically.** `host_key` is
   part of Chrome's cookie unique key, so the dotted row written by fix #2 does
   not *replace* the dotless row an older injector wrote — they coexist, and on
   an apex request the reader sends both with the stale one ordered first (RFC
   6265). The apex then authenticates with a dead token while subdomains work.
   `staleHostOnlyTwins` deletes the shadowed row before each write. **It must
   stay surgical:** a host-only cookie sharing a name with a domain cookie is
   *not* automatically stale — this sink's source carries eight legitimate pairs
   (`OptanonConsent`, `__cuid`, `oai_consent_*`, …), and a blunt "delete the
   dotless twin" destroys them. Guards: the `TestStaleHostOnlyTwins_*` unit tests
   and `TestLiveInject_ConvergesStaleHostOnlyTwin`.

A purged profile fails *silently and asymmetrically*: it looks correctly seeded
on disk, and the reader's own fresh anonymous cookies survive while the synced
auth rows vanish. Never conclude the lane works from a row count taken before the
reader has opened the profile.

The profile at `~/.agentcookie/chrome-profile` is **long-lived and never wiped**,
so any injector change that alters how a row is keyed (`host_key`, name, path)
leaves the old rows behind rather than updating them. Ask what happens to the
rows already on disk, not just what the next write produces.

## Never push an expired cookie

Setting a cookie whose expiry has passed is a **delete**, in Chrome
(`Network`/`Storage.setCookies`, and a SQLite upsert Chrome then discards)
and in WebKit (cmux). The source ships every row with its original expiry,
so before 2026-10-25 this sink re-pushed the laptop's dead rows every ~30s and
wiped the live cookies the destination browser had minted for itself --
Apple's `shld_bt_*` shield cookies in the human Chrome, so Add to Bag
answered HTTP 541 ("Page Not Found").

Every push path now drops rows with a non-zero `ExpiresUTC` at or before
now + `chrome.PushExpiryMargin` (2 min, because a push is not instant):
`livecdp.BuildCookieParams`, `cdp.InjectCookies`, `chrome.WriteCookies`,
`sinkpush.RunAll`, `CmuxAdapter.Push`. Session cookies (`ExpiresUTC == 0`)
still push. The sidecar `cookies-plain.db` stays a verbatim mirror -- it is
not a push. The cold injector's twin convergence still sees the full source
set, so a host-only row the source carries only in expired form keeps
protecting the reader's live twin. The sink log shows the count per push:
`... pushed N cookies (skipped M expired) ...`. Guards:
`TestSinkSync_NeverPushesExpiredCookies`, `TestBuildCookieParams_SkipsExpiredKeepsSession`,
`TestCmuxPush_SkipsExpiredCookies`, `TestRunAll_SkipsExpiredCookies`,
`TestWriteCookies_ExpiredNeverOverwritesLiveRow`,
`TestStaleHostOnlyTwins_ExpiredSourceRowStillProtects`.

## Tests

```bash
go test ./...                                   # hermetic
go test -tags livechrome ./internal/cdp/...     # spawns real headless Chrome
```

The `livechrome` tests in `internal/cdp/roundtrip_live_test.go` seed a throwaway
profile and assert against the resulting SQLite that domain scope survives and
that rows are sealed under the automation key. They are the regression guards for
both defects above; they fail on the pre-fix commit.

`internal/cdp/localstorage_live_test.go` is the guard for the localStorage
receiver. It seeds a fake "laptop" profile through Chrome, packs it the way the
source does, installs it into a second profile that already has its own
localStorage, and asserts the three properties that make the receiver safe to
point at a long-lived reader profile: the synced origin arrives, a blocklisted
origin does not, and the reader's own origins survive. Swap the per-origin write
for a directory replace and the last two go red.

## Rebuild / install / bounce

```bash
cd ~/Workspace/tools/agentcookie
make build                                       # -> ./bin/agentcookie (arm64)
launchctl bootout gui/$(id -u)/dev.agentcookie.sink
cp ./bin/agentcookie ~/.local/bin/agentcookie
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.agentcookie.sink.plist
```

Before swapping a binary, sanity-check that the new one parses the live config:
run it against a *copy* of `~/.config/agentcookie` and confirm it gets as far as
`bind: address already in use` (that means `LoadSink` succeeded). Restarting
resets the cosmetic lifetime counters in `sink-state.json`; `sequence.json`
replay state survives, and that is the one that matters.

## End-to-end verification (the only check that counts)

**Stop the sink first.** It re-injects every ~30s, and a concurrent re-injection
will mask a purge and hand you a false pass.

```bash
launchctl bootout gui/$(id -u)/dev.agentcookie.sink
# snapshot row counts from ~/.agentcookie/chrome-profile/Default/Cookies
agent-browser close --all
agent-browser open https://www.airbnb.com/trips --profile ~/.agentcookie/chrome-profile
# expect: no /login redirect, real trips in `agent-browser snapshot`
agent-browser close --all
# re-read the DB: row count must be unchanged, auth rows still `.airbnb.com`
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.agentcookie.sink.plist
```

Cookies only flush to SQLite on a clean browser shutdown, so always
`agent-browser close --all` before the post-launch read.

## The cmux lane reuses an open browser tab -- it does not add one

`browser.cookies.set` needs a browser `surface_id`, but WebKit cookies land
at the profile level, so any open browser surface works. Before 2026-10-30
the adapter (`internal/sinkpush/adapter_cmux.go`) added its own unfocused
`about:blank` tab to the first workspace every time its cache was empty --
every sink restart and every time Rony closed it -- and stray "New tab"
browsers piled up.

Now `ensureSurfaceLocked` reads `cmux tree --all --json` and reuses an open
browser surface (an about:blank one first, else any), in any workspace. It
adds a tab only when no browser surface is open at all, or when `tree` is
unusable but cmux is reachable. The stale-cache reopen path does the same
and skips the ref that just failed.

**A reused tab may be a live page** (eos-scorecard, a Triple Whale
session). The injection path only ever calls `browser.cookies.set` on it --
never navigate, reload, focus or close. The doctor/cmux-sync auth probe
(`verify.go`) navigates, so it keeps opening and closing its own surface;
do not "unify" it onto the reuse path. The opt-in live test only closes a
surface that was not open before its Push. Guards:
`TestCmuxPush_ReusesOpenBrowserSurface`,
`TestCmuxPush_ReusesLivePageWhenNoBlankTab`,
`TestCmuxPush_StaleCacheReusesAnotherSurface`,
`TestCmuxPush_NoBrowserSurfaceAddsOne`.

### Small frames, retries, delta push, and dead restored shells (2026-11-02)

cmux 0.65.2's socket drops request frames bigger than **~4KB** at random
("Socket closed before reply" / "Broken pipe"), and a dropped call lands
nothing. Measured with the sink stopped: params up to 3818 bytes failed 0 of
75 calls; from ~3900 bytes up, 15-65% failed at every size up to 334KB, no
matter the cookie count. (The first diagnosis, a ~512KB cap, came from too
few samples.) 200-cookie batches are ~60-80KB, so cmux delivery was dead
from the upgrade until this fix. Now:

- `cmuxChunks` bounds each `browser.cookies.set` params JSON at
  `cmuxMaxParamsBytes` (3500). A cookie bigger than that rides alone.
- `setCookiesLocked` resends a dropped frame up to `cmuxTransientRetries`
  times (`isTransientSocketError`). Broken pipe is **not** "cmux down" any
  more; a chunk that drops through every retry is left unrecorded, the push
  carries on, and Push returns a `N of M cookies not delivered` error.
- **Delta push.** A full push is now ~300 calls (~19ms each) inside the
  `/sync` handler, so the adapter fingerprints (sha256, no values held) what
  landed and resends only changed cookies. The record resets on a surface
  change and every `cmuxFullResync` (15 min). A positive WebKit reject
  (`Invalid cookie payload`) is recorded too, so an unchanged reject is not
  re-sent each push; any unclassified failure stays unrecorded and is resent. Side effect: a
  cookie the cmux browser rotated itself is no longer clobbered by the
  laptop's copy every 30s, only on the 15-min resync or a source change.

Round-2 honesty fixes (council, 2026-11-02):

- **cmux down is classified as down.** cmux 0.65 with no socket says
  `Socket not found at <path>`; hung, it says `Socket connection deadline
  exceeded` / `Socket read error`. All three are `isCmuxUnavailable` --
  one hard-fail, never a per-cookie exec storm under the push lock.
  `Socket closed before complete reply` is a retried transient.
- **The log says what was sent.** `PushCounted` (the optional
  `CountingPusher` interface RunAll prefers) feeds `adapter cmux pushed N
  changed of M (U unchanged, R rejected)`. A settled WebKit reject keeps
  being counted on every line. A push with nothing pending still probes
  the cached surface (`browser.url.get`), so a quit cmux is a FAIL line,
  not "pushed 312 cookies"; a dead cached surface reopens and full-pushes.
- **One cookie per WebKit key.** Chrome's unique key is wider than
  host/name/path, so the input is deduped (last wins) before the delta --
  otherwise two rows would alternate in the record and flap the jar value.
- **Callers hand Push the full set.** The resync only heals jar-side drift
  if the adapter sees unchanged cookies; `cmux-sync --watch` dropped its
  own pre-filter for that reason.

cmux session-restore resurrects browser tabs as uninitialized shells: `tree`
lists them as `browser` with an empty url -- exactly what reuse prefers --
but every `browser.*` rpc fails `Surface is not a browser`. Reuse now probes
each candidate with the read-only `browser.url.get` and skips only those
that fail with a surface error; any other probe failure counts as alive, so
an older cmux never degrades into a tab per push.

Guards: `TestCmuxPush_RealisticSetStaysUnderFrameCap`, `TestCmuxChunks_*`,
`TestCmuxPush_RetriesDroppedFrame`,
`TestCmuxPush_PersistentDropSkipsChunkKeepsGoingAndResends`,
`TestCmuxPush_Delta*`, `TestCmuxPush_SkipsDeadRestoredShells`,
`TestCmuxPush_ProbeNonSurfaceErrorStillReuses`,
`TestCmuxPush_QuitOrHungCmuxHardFailsFast`,
`TestCmuxPush_ClosedBeforeCompleteReplyIsRetried`, `TestCmuxPushCounted_*`,
`TestRunAll_CountingPusherReportsWhatWasSent`, `TestAdapterPushedLine`,
`TestCmuxPush_DuplicateKeyDoesNotFlap`.

Upstream (with pull mode) made cmux a per-apply adapter: `cmuxSurfaces`
in `internal/cli/sink.go` keeps one adapter per (binary, filter) and swaps
it via `Reconfigured` when sink.yaml changes. `Reconfigured` carries the
delta record along with the tab, or every settings change would cost a
full push (`TestCmuxAdapter_ReconfiguredKeepsDeltaRecord`). Every cmux
call also runs under the caller's ctx now (`PushCounted(ctx, ...)`), so
pull-once's budget can stop a stalled push.

Caveat: cmux exposes no per-surface browser profile in `tree`. If a
non-default `--profile` browser tab is ever the only one open, cookies would
land in that profile's store.

## localStorage: how it reaches this sink (and how it must never reach it)

Upstream has carried localStorage + IndexedDB sync since v0.7 (protocol v2,
`LocalStorageTarball` / `IndexedDBTarball`), but **none of it reached this
machine until 2026-09-18**, for three independent reasons worth keeping in
mind because two of them are still true:

1. `skip_chrome_sqlite: true` routes every `/sync` through
   `applySidecarOnlyToSink`, which writes the cookie sidecar and returns.
   Both tarballs were decoded off the wire and dropped. **Still true for
   IndexedDB.**
2. `applyEnvelopeToSink` -- the path this sink does NOT take -- installs
   the tarballs into `chromepaths.LocalStorageLevelDB()`, i.e. the
   machine's **real** Chrome Default profile, not
   `~/.agentcookie/chrome-profile` where `agent-browser` reads. **Still
   true**; that function is for a universal-delivery sink.
3. The CDP injector was cookies-only. **Fixed:** `cdp.local_storage: true`
   turns on `internal/cdp.InstallLocalStorage`, which installs the synced
   localStorage into the CDP profile per origin.

### The receiver, and why it is shaped the way it is

`InstallLocalStorage` stages the tarball in a throwaway user-data-dir,
launches one headless Chrome there to read each origin's items, then
launches a second against the CDP profile and writes them back with
`localStorage.setItem`. Both Chromes intercept every request through
`Fetch.fulfillRequest` and serve an empty local document, so seeding an
origin costs **no network request** -- the sink must not phone out to
app.adnova.ai to write a storage area, and a site being down must not take
its own localStorage install down with it.

Four things are load-bearing, all measured rather than assumed:

- **Navigation is required.** `DOMStorage.setDOMStorageItem` on an origin
  with no loaded frame fails with `Frame not found for the given storage
  id (-32000)`. Hence the intercept-and-navigate dance; it is not
  ceremony.
- **The close must be graceful.** localStorage lives in memory until
  Chrome shuts down cleanly, exactly like the cookie store. Measured:
  without `chromedp.Cancel`, a value written here reads back `null` on the
  next open. The deferred context cancel SIGKILLs and loses the write.
- **Never swap the directory.** `chromedirsync.AtomicReplaceDir` at this
  profile is the one thing that must not happen.
  `TestLiveInstallLocalStorage_RoundTripIsAdditiveAndHonorsBlocklist`
  fails two ways when you try it: the reader's own accumulated origins are
  destroyed (the profile is long-lived and never wiped), **and the
  blocklist is bypassed entirely** -- a tarball carries every origin the
  laptop had, so a whole-dir install has no place to filter. Per-origin
  writes are the only shape that can honor the cookie policy.
- **The blocklist is the same predicate as cookies.** mercury/google/
  shopify/meta must not gain a localStorage side door; for a modern app
  the localStorage token *is* the session. Origins whose host will not
  parse are dropped, not allowed -- fail closed.

Re-installing is skipped when the tarball is byte-identical to the last
one (`sha256`, per-mux, in memory). This runs after every `/sync` (~30s),
and localStorage changes far less often than cookies; without the check it
would spend two Chrome spawns a minute rewriting the same bytes.

**IndexedDB still goes nowhere** on this sink, and it only rides at all
when the source sets `AGENTCOOKIE_SYNC_INDEXEDDB=1`. A `cdp_source`-mode
source packs neither tarball by design.

**Device-bound tokens (Triple Whale, OpenAI class) may not replay even
once their localStorage is synced.** That is expected, not a bug -- those
sites bind the session to more than storage.

**The leveldb under `~/.agentcookie/chrome-profile/Default/Local Storage/`
is not all synced data.** `agent-browser`'s Chrome for Testing writes its
own there during agent sessions. Do not read an origin's presence as proof
the sync works; and note a `META:` key in `MANIFEST-*` only means the
origin existed at *some* point -- MANIFEST retains key boundaries for
tables compacted away, which is why `originsInLevelDB` reads only `.ldb`
and `.log`.

## The extra-profile cookie union is gated OFF here

Upstream's `unionCookiesWithExtraProfiles` scans this Mac's own Chrome/
Chromium/Brave/Edge/Dia roots and tries `getChromeDecryptKey` (a
10s-timeout `security find-generic-password`) per browser, inside the
`/sync` request handler. On a sink whose entire posture
(`skip_chrome_sqlite: true`) exists to avoid Safe Storage, that is
backwards: it is inert today only because the key reads fail, and if the
keychain ever became readable, personal-browser cookies would flow into
the synced set with no off switch.

It is gated on the mode, not a new knob, so a normal universal-delivery
sink is unaffected. Upstream shipped the same gate independently (#137/#138:
`safeStorageReadAllowed` in `adapterCookieSet`, plus a per-browser cache of
permanent Keychain refusals in `getChromeDecryptKey`); the 2026-11-09
reconcile dropped our duplicate gate and cache in favor of theirs. Ours
still adds: a merge that actually happens is logged, the union goes through
the `unionExtraProfiles` test seam, and the agentcookie CDP profile is excluded from
the union: it is sealed under the automation mock-keychain key, not Safe
Storage, so reading it with this machine's real Chrome key yields nothing
-- and it is already the destination of the CDP write path.

## Diagnostics: the log must not state a fact about a surface it never touched

`sink.err.log` printed `+ 0 localStorage origins + 0 indexedDB origins` on
every one of 89,839 syncs, on a path that writes no leveldb at all. It
reads as "the source sent none" and it cost a full diagnosis pass aimed at
the wrong machine. The rules now:

- The skip path never prints a bare zero. It says what happened to the
  payload: installed, `DISCARDED`, or `nothing to discard`.
- `DISCARDED` is reserved for bytes that actually arrived and were
  actually dropped. Claiming a discard when nothing was sent is the same
  misdiagnosis pointing the other way.
- The envelope-shape line logs **on change**, keyed by
  (source, protocol version, ls present, idb present) -- not once per
  process. Once-per-process freezes a possibly-wrong first answer for the
  daemon's lifetime and misses a second source entirely.
- The ok-line is the sink's reply body and the **source** prints it into
  its own log, so it carries the same disposition -- otherwise the laptop
  half stays blind.
- The source logs the tarball sizes it packed, so "my capture is broken"
  and "the sink dropped it" are distinguishable from either end.

## `delivery: degraded` is correct here -- do not "fix" it

`delivery` in `sink.yaml` is a **descriptive marker only**: the wizard
records which install shape was chosen and `doctor` reads it back
(`internal/cli/doctor.go`). Nothing branches on it. `degraded` is the
truthful label for this machine -- doctor renders it as "writes a separate
profile; only agentcookie-aware tools work", which is exactly the intended
design (sidecar + `~/.agentcookie/chrome-profile`, real Chrome untouched).

Setting it to `universal` would not restore anything. Universal delivery
means writing the machine's real Chrome profile, which needs a Chrome Safe
Storage Keychain prompt that a headless mini has no GUI session to answer --
the reason `skip_chrome_sqlite` was set in the first place.

## `/pull` is opt-in and sink-scoped (local patch, 2026-11-09)

Upstream's pull mode made every `source --watch` serve `GET /pull` on
100.x:9998 and authorized any file under `keys/` plus the legacy shared
secret. A sink retired from `sinks:` keeps its key file (no unpair for
plain peers), so it kept pulling the whole jar. Now: the listener starts
only with `pull_listener.enabled: true` in source.yaml or a listed
teammate (`pullListenerWanted`); a plain key must belong to a peer in
`ResolvedSinks()` (`sinkPeerNames`); the legacy secret is refused. This
is the laptop (source) half; the mini sink does not serve `/pull`.
Guards: `TestPullRefusesKeyOfSinkNotInSinksList`,
`TestLegacySharedSecretRefusedOnPull`, `TestPullListenerOffWithoutOptIn`,
`TestPullListenerServesListedSinkWhenOptedIn`.

## The live-CDP stale-tab sweep must re-read before every close

`livecdp.SweepStaleBlankTabs` (started by `runSink` and by `sink
--pull-loop`) closes `about:blank` tabs older injectors leaked into the
shared Chrome. It batches full target listings (one per 50 closes) for
speed, so every candidate is re-read with `Target.getTargetInfo`
immediately before its close and skipped if another client navigated,
attached to or closed it. Do not drop that per-close read to "save a
call"; a stale listing closes live tabs. Guards:
`TestCloseStaleBlanks_SparesTabChangedMidBatch`,
`TestRunSinkStartsLiveCDPSweep`, `TestTeammatePullLoopStartsLiveCDPSweep`.
Upstream PRs: mvanhorn/agentcookie #156 (livecdp) and #157 (cmux reuse).

## Known rough edges (not fixed)

- `sink.err.log` is unrotated and was ~44 MB as of 2026-08-13.
- The injector and `agent-browser` both open the same `user-data-dir`; a sync that
  lands while the browser is open contends for the profile lock.
- Some sites bind sessions beyond cookies (OpenAI, Triple Whale) and will not ride
  this lane regardless. Context for what does and does not ride lives in the vault
  atom `browse-as-rony-via-agentcookie`.
