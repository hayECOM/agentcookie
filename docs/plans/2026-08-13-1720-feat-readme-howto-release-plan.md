---
title: "feat: README how-to + 1.0 release pipeline"
status: active
type: feat
created: 2026-08-13
---

# feat: README how-to + 1.0 release pipeline

## Goal

Ship a README how-to that makes Mac to Grok Bot Linux cookie sync the featured path, lockstep satellite docs/skill/release notes, and a linux+darwin binary pipeline so a later human-approved `v1.0.0` can attach the right archives. Celebrate live CDP over Tailscale.

Stop when U1-U5 are on a PR against main. Do not merge. Do not change sink inject, policy defaults, or pairing crypto.

## Authority

This prompt is the execution brief. Investigate the repo and reach your own conclusions. Session-settled product decisions below are structure pins (do not "improve" them). A real defect inside a settled approach should still be surfaced, not silently worked around.

## Session-settled (do not reverse)

- Featured path is Grok Bot Linux sync, not Mac-mini-first.
- Formal release target is GitHub Release `v1.0.0` with binaries (this PR prepares it; does not cut it).
- Transport stays Tailscale `/sync` only. No export/import file path in the happy path.
- Happy-path cookie set is sync-all via explicit YAML override, not Amazon-only.
- Live CDP attach on Linux. Do not write Chrome cookie SQLite as success.
- Do not change `PolicyModeForSink` or any sink inject code.

## Hard denylist (must not change)

`internal/config/allowlist.go`, `internal/protocol/allowlist.go`, `internal/cli/sink.go`, `internal/livecdp/*`.
No cookie values in any file. No CDP on the tailnet. Do not start a second Chrome / agent-sync `:9400`. Do not flip `RELEASE_CI_ENABLED`. No Homebrew. No `web/` marketing lockstep.

## Current binary facts you must document (do not "fix" in code)

1. Linux omitted `policy:` is allowlist-empty (ships nothing). Featured how-to writes `~/.config/agentcookie/blocklist.yaml` with `version: 1`, `policy: blocklist`, `domains: []` for a single-operator Grok Bot / trusted box. Other Linux sinks keep the default. 1.0 notes must NOT claim "Linux defaults to sync-all". A later PolicyModeForSink flip is a breaking follow-up.
2. Wizard on Linux is forbidden in the featured path. `wizard install --as sink` omits `policy:` (ships nothing) and can write `cdp.enabled: true`, which makes `applyLinuxSinkDefaults` skip live CDP and can spawn a second Chrome that fights Grok Bot. Document the YAML, not "run wizard and done".
3. Frozen Linux YAML:
   - `sink.yaml`: `listen.addr` on 100.x:9999, `peer.hostname` = Mac Tailscale name, `live_cdp.enabled: true`, `live_cdp.endpoint` default `http://127.0.0.1:9223`, `cdp.enabled: false` (or omit `cdp.enabled`; never leave wizard `cdp.enabled: true`), `skip_chrome_sqlite: true`. Policy does NOT live in sink.yaml (KnownFields rejects it).
   - `blocklist.yaml`: `version: 1`, `policy: blocklist`, `domains: []`.
4. Attach to the already-running box Chrome. Never `cdp.managed` / LaunchOwnedChrome / `:9400`.
5. Doctor can print `sync-all` while `/sync` drops everything. Verify with ok-line `live_cdp: injected N cookies into M context(s)` and `LastWriteMode` containing `livecdp`, not the policy label. Linux `wrote 0 cookies` is expected. Sidecar is not success.
6. Default CDP port 9223; doctor also probes 9222/9224/9228/9229/9400. How-to must say what to do when Chrome is on 9228.
7. Pairing: Mac `wizard install --as source --peer <linux-tailscale-name>`; user relays the pairing code (10-minute, not a cookie); Linux `pair` with `--code` and `--pair-url`. Cookie values must never appear.
8. Keep the sink alive: copy the wizard-printed systemd user unit (do not auto-install). Fresh browserUse only works while the sink is still polling.

## Units (do all of these)

### U1 README.md

Re-lead with Mac to Grok Bot Linux forever-sync. Demote second-Mac / Mac mini below the fold (still supported, not first).
Reading order from `docs/plans/2026-05-22-001-feat-marketing-readme-rewrite-plan.md`: outcome, proof, why, how, install, honest Status, docs table, MIT.
Featured how-to is agent-executable: numbered CLI primitives, `doctor --json` / `status --json`.
Honest Status: Linux SQLite write is 0; look for live_cdp inject; omitted policy ships nothing; Google/DBSC local sign-in; CDP loopback only; same-user processes can attach to the debug port; sidecar `~/.agentcookie/cookies-plain.db` is plaintext and is not success; never `cookies --json`. Linux doctor table: expected FAIL/WARN (codesign, Chrome.app, launchctl) vs ignore vs must-fix (Live CDP endpoint, tailnet bind).
Install: GitHub Release binaries first; verify against `checksums.txt`; `go install ...@v1.0.0` after the tag. No Homebrew. Do not point at `docs/quickstart.md` as the 1.0 how-to (stale).
Freeze download names: `agentcookie_1.0.0_linux_amd64.tar.gz`, `agentcookie_1.0.0_linux_arm64.tar.gz`, plus one darwin-arm64 tarball matching the frozen scheme.

### U2 satellites

Files: docs/faq.md, docs/consumption.md, docs/architecture.md, docs/threat-model.md, examples/sink.yaml, CHANGELOG.md

- FAQ Linux Q matches featured YAML. Fix Apache 2.0 vs MIT. Fix Docker paragraph that still says Linux sink has not landed.
- consumption.md: keep Grok Bot example; sidecar/adapters/cookies DO run on Linux; sidecar is plaintext at rest; never `cookies --json` as verify; success is live inject.
- architecture.md + threat-model.md: keep allowlist-empty as code default / 1.0 security-by-default; featured Grok Bot path is the explicit `policy: blocklist` override.
- examples/sink.yaml: commented `live_cdp` attach at 9223, not `cdp.managed` LaunchChrome. Must not be copy-pasteable into a second Chrome. This file ships in the GoReleaser archive.
- CHANGELOG: add `## [1.0.0] - 2026-08-13` for the hero + Linux binaries. Do not trust `[Unreleased]` as the 1.0 body.

### U3 skill

Files: skill/SKILL.md, skill/prompts/install-on-both-machines.md

Remove "Linux sink support (planned for v0.3)" and Mac-mini-SSH-as-only-sink assumptions.
Two-agent playbook: Mac source wizard; Grok Bot does NOT run `wizard install --as sink`; writes featured YAML and pairs with flags + user-relayed code.
Verify with doctor/status JSON + LastWriteMode / live CDP counts. Never cookies --json.

### U4 notes

Files: .github/RELEASE_NOTES_TEMPLATE.md, CHANGELOG.md

Replace closed-beta / macOS-only template. Name real assets, Tailscale-only, live CDP attach, linux sqlite-0 expected, doctor as support, DBSC/Google local sign-in, cookie values never logged.
Draft the handwritten 1.0 header (highlights, install, breaking/honest limits, security). Do not paste the full PR list into the template (GitHub-native notes from v0.17.1 are appended at cut time).
No claim that Linux defaults to sync-all.

### U5 packaging

Files: .goreleaser.yaml, .github/workflows/release.yml, scripts/release-tarball.sh, scripts/install-beta.sh

Linux archives need CGO_ENABLED=1 (go-sqlite3 has no build tags). Build on Ubuntu: amd64 with default gcc, arm64 with CC=aarch64-linux-gnu-gcc. Do NOT add linux to the current darwin GoReleaser job (CGO + sign.sh on macos-latest will fail). Isolate any linux GoReleaser id so macos-latest never builds it. Keep darwin Path A + codesign unchanged.
Fix release.yml to `go-version-file: go.mod`. Do not flip RELEASE_CI_ENABLED.
Change release-tarball.sh to emit `agentcookie_1.0.0_darwin_arm64.tar.gz` (underscores, no leading v) and update install-beta.sh to `--pattern "*darwin_arm64.tar.gz"` so there is one scheme.
One publisher. No post-publish --clobber. checksums.txt covers every attached archive.

## Verification (required)

- `go build ./...`, `go vet ./...`, `go test ./...`
- `goreleaser check` after U5
- Grep-clean across README, docs/, skill/, .github/RELEASE_NOTES_TEMPLATE.md, examples/ for: "macOS only on both ends", "Linux ... on the roadmap", "v0.2 Linux sink work needs to land", "Closed-beta", "invitation only", "Linux sink support (planned for v0.3)"
- No cookie values in those files
- Relative README links resolve
- Do not change the denylisted sink files

## Done

PR against main with U1-U5. Title/body should say this prepares v1.0.0 and does not cut the tag. Report the PR URL, changed files, grep results, and test/goreleaser results.

Hypothesis only (verify, discard if wrong): examples/sink.yaml currently has cdp.enabled/managed that would skip live CDP if copied; U2 must rewrite that, not only add a comment.
