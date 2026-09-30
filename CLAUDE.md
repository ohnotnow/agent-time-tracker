# CLAUDE.md

`att` reads Claude Code and Codex session logs and shows how much of a session the agent spent working versus waiting on the human. It has a terminal view, a `--follow` mode and a live web UI (`att serve`).

Read `TECHNICAL_OVERVIEW.md` before changing anything in `internal/att/`. It documents the log format quirks we found the hard way, and how turns are split into work and waits.

## Commands

```bash
go build -o att ./cmd/att
go test ./...
./att                  # latest session for the current directory
./att --follow
./att serve            # http://127.0.0.1:8080
```

## Layout

```
cmd/att/main.go            CLI flags, log resolution, --follow loop, `serve`
internal/att/session.go    shared: Build, Load, FindSession, ait matching
internal/att/claude.go     Claude Code log -> turns, and finding its sessions
internal/att/codex.go      Codex log -> turns, and finding its sessions
internal/att/timeline.go   turns -> work/wait segments, issue totals
internal/att/rows.go       display rows shared by terminal and web
internal/att/render.go     terminal output
internal/web/server.go     serves the page and /api/timeline
internal/web/static/       embedded index.html (plain JS)
```

## Rules of the road

- Standard library only. No third-party Go modules.
- Each agent's log format lives only in its reader (`claude.go`, `codex.go`), which produces plain `Turn`s. Everything from `segment()` onwards is shared and must not check which agent wrote the log.
- A new agent means a new reader file, a branch in `Build` and a finder in `FindSession`. No plugin or registration machinery.
- Wait/work decisions live in `rows.go` and `timeline.go`, so the terminal and web views cannot disagree. Don't add view-specific logic that reinterprets the timeline.
- Both log formats are internal and undocumented. When one surprises you, check a real log, then add the case to the fixture (`timeline_test.go` for Claude, `codex_test.go` for Codex) and a line to `TECHNICAL_OVERVIEW.md`.
- Subagent and injected traffic must never count as human prompts or stretch a turn (Claude: `isSidechain`, `isMeta`; Codex: subagent sessions are separate files, skipped by `FindSession`).
- Codex's `duration_ms` includes time waiting on approvals, so it is not Claude-style active time. Codex turns are `Exact: false`.
- The web UI shows snippets of the human's messages, so the default listen address stays `127.0.0.1`.
- Real session logs contain private conversation text. Don't copy them into tests, fixtures or commits; hand-build fixtures instead.

## Releases

`.github/workflows/release.yml` builds binaries when a `v*.*.*` tag is pushed.
