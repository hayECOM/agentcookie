# CLAUDE.md — agentcookie (vendored third-party, locally patched)

**This is not our code.** It is a clone of the public upstream
`github.com/mvanhorn/agentcookie` (MIT), carrying a local patch that is not
upstreamed yet. Treat upstream files as someone else's house style: match what is
already there, keep diffs small, and keep every local change on a topic branch so
it stays PR-able.

- Upstream remote: `origin` → `github.com/mvanhorn/agentcookie`
- Local patch branch: `fix/cdp-sink-keyring-and-domain-scope` (merged into local `main`)
- Installed binary: `~/.local/bin/agentcookie` (arm64), built from local `main`

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

A purged profile fails *silently and asymmetrically*: it looks correctly seeded
on disk, and the reader's own fresh anonymous cookies survive while the synced
auth rows vanish. Never conclude the lane works from a row count taken before the
reader has opened the profile.

## Tests

```bash
go test ./...                                   # hermetic
go test -tags livechrome ./internal/cdp/...     # spawns real headless Chrome
```

The `livechrome` tests in `internal/cdp/roundtrip_live_test.go` seed a throwaway
profile and assert against the resulting SQLite that domain scope survives and
that rows are sealed under the automation key. They are the regression guards for
both defects above; they fail on the pre-fix commit.

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

## Known rough edges (not fixed)

- `sink.err.log` is unrotated and was ~44 MB as of 2026-08-13.
- The injector and `agent-browser` both open the same `user-data-dir`; a sync that
  lands while the browser is open contends for the profile lock.
- Some sites bind sessions beyond cookies (OpenAI, Triple Whale) and will not ride
  this lane regardless. Context for what does and does not ride lives in the vault
  atom `browse-as-rony-via-agentcookie`.
