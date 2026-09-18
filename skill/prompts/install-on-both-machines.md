# The "tell your agent this" prompt

Copy this into Claude Code, OpenClaw, Hermes, Codex, Cursor, or any agent that can run shell commands locally and over SSH:

> Install agentcookie on this Mac and my Linux agent box so my Chrome sessions sync continuously. Use Tailscale to find the Linux box. Confirm with me which machine is the source and which is the sink, then run the full install end to end. After install, verify both sides are running and tell me what you see.

The agent should:

1. Detect Tailscale and identify the peer machine.
2. Confirm source vs sink with you.
3. Run `agentcookie wizard install --as source` here on the Mac, in the background.
4. Read the pairing code from `~/.agentcookie/pairing.json` once it appears.
5. SSH to the Linux box and:
   - Install the agentcookie binary
   - Write `sink.yaml` with `live_cdp.enabled: true` and the tailnet IP
   - Write `blocklist.yaml` with `policy: blocklist` and `domains: []`
   - Run `agentcookie pair --as sink` with the code and URL
   - Start Chrome with `--remote-debugging-port=9223`
   - Start `agentcookie sink`
6. Report back that both sides are up and show the verify output.

Total elapsed time: about 60 seconds. You do not need to be at the Linux box's screen.

## For Mac-to-Mac instead of Mac-to-Linux

> Install agentcookie on this Mac and my Mac mini so my Chrome sessions sync continuously. Use Tailscale to find the Mac mini. Confirm which is source and sink, then run the full install end to end.

The wizard works on macOS sinks:

```bash
ssh <mac-mini-hostname> "agentcookie wizard install --as sink \
  --peer <source-hostname> \
  --code <code> \
  --pair-url <url>"
```

## When the prompt is not enough

If the agent gets stuck, the most common reasons (in rough probability order):

1. SSH from your Mac to the Linux box does not work passwordlessly. Fix by setting up SSH keys, or use Tailscale SSH (`tailscale ssh`).
2. Google Chrome is not installed on the Linux box. Install Chrome.
3. Chrome is not running with `--remote-debugging-port`. Start it with the debug port.
4. Tailscale ACLs are restrictive. Default Tailscale config allows everything between your own devices; if you have custom ACLs, allow tailnet-internal traffic on ports 9998 (pairing) and 9999 (sync).

After fixing, re-paste the prompt. The install is mostly idempotent.

## Verifying success

On Mac:
```bash
agentcookie doctor
agentcookie status --json
```

On Linux:
```bash
agentcookie doctor
agentcookie status --json
```

Look for:
- `live_cdp: endpoint reachable` OK
- `tailnet: bind address` OK
- `LastWriteMode` containing `livecdp`
- `live_cdp: injected N cookies into M context(s)` in sync output

Ignore expected FAILs on Linux: codesign, Chrome.app path, launchctl.

The message `wrote 0 cookies` is expected on Linux. Success is the live CDP inject line, not the sidecar or SQLite write.
