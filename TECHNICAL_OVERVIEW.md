# Technical overview

Last updated: 2026-09-30

## What this is

`att` reads a Claude Code or Codex session log and shows how much of the session the agent spent working and how much it spent waiting on the human.

## Stack

- Go 1.27, standard library only (no third-party modules)
- The web page is a single embedded `index.html` with plain JavaScript, and Google Fonts as its only external resource

## Directory structure

```
cmd/att/main.go            CLI: flags, resolving which log to read, --follow loop, `serve`
internal/att/session.go    shared: Build, Load, FindSession, spotting ait commands
internal/att/claude.go     reads a Claude Code log into turns; finds its sessions
internal/att/codex.go      reads a Codex log into turns; finds its sessions
internal/att/timeline.go   splits turns into work and waits, credits issues
internal/att/rows.go       flattens a timeline into display rows shared by both views
internal/att/render.go     terminal output
internal/web/server.go     HTTP server: serves the page and /api/timeline as JSON
internal/web/static/       the embedded page
```

The split is deliberate. Each agent's reader is the only code that knows its log format, and it hands back plain `Turn`s. `Build` picks the reader (a log whose first line is `session_meta` is Codex's, anything else Claude Code's), then runs the shared code on the turns. `rows.go` decides what counts as a wait and what ended it, so the terminal and the web page cannot disagree.

## Finding the session

`FindSession` looks for the newest session for a project directory from either agent: Claude Code's newest log for the project, and any Codex session modified after it. With both agents in use in the same directory, `--follow` and the web page will switch between them as each writes.

## The Claude Code session log

Claude Code writes one JSON object per line to `~/.claude/projects/<dir>/<session-id>.jsonl`. `<dir>` is the project's absolute path with every non-alphanumeric character replaced by `-` (`latestClaude` in `claude.go`). "The latest session" means the most recently modified `.jsonl` in that directory.

This is Claude Code's internal format, not a documented interface. Everything below was worked out by reading real logs and may change with a Claude Code release. Lines that fail to parse as JSON are skipped silently.

### Entries that matter

| Entry | What it means to `att` |
|-------|------------------------|
| `type: "user"` with `origin.kind: "human"` | A message the human typed. Starts a turn. |
| `type: "user"` whose content starts with a `tool_result` block | A tool call finishing. Matched to its call by `tool_use_id`. |
| `type: "user"`, not `isMeta`, not a tool result, no human origin | A slash command or similar. Only starts a turn if an assistant entry follows. |
| `type: "assistant"` | Agent output. Its `tool_use` blocks record tool calls, each with an `id`, `name` and `input`. |
| `type: "system"`, `subtype: "stop_hook_summary"` | Stop hooks ran, so the turn is ending. |
| `type: "system"`, `subtype: "turn_duration"` | The turn ended. `durationMs` is Claude Code's own measure of active time. |

Everything else is ignored. Real logs also contain many entries of other types (`attachment`, `mode`, `permission-mode`, `ai-title`, `file-history-snapshot`, `queue-operation`, `system` with `subtype: "away_summary"` and more).

### Details that caught us out

- `message.content` is sometimes a plain string and sometimes a list of blocks. A message with an image attached arrives as a list with `text` and `image` blocks. `entry.text()` handles both.
- `isSidechain: true` marks subagent traffic. These entries are skipped entirely, so a subagent's `ait claim` does not count against the main session, and its timestamps don't stretch the turn.
- `isMeta: true` marks entries Claude Code injected itself, such as the `<local-command-caveat>` line that follows a slash command. A message from another agent session (a subagent hand-back) also arrives as a `user` entry, with `origin.kind: "peer"` and `isMeta: true`, so it never counts as a human prompt.
- A slash command logs as a `user` entry with content like `<command-name>/permissions</command-name>` and no human origin. If the human then types a message, the turn starts at the message, not the slash command.
- `turn_duration` already excludes time spent in question dialogs and permission prompts, so the wall clock minus `durationMs` is the time the agent was blocked on the human. `timeline.go` uses that leftover as a budget (see below).
- In the logs checked, `stop_hook_summary` came immediately before `turn_duration`. Older logs, or turns where it is missing, may have only one of the two. `att` closes the turn on whichever comes first. If `turn_duration` follows a turn already closed by a stop hook, it replaces the wall-clock estimate with the exact figure. A turn with no `turn_duration` is marked approximate (`Exact: false`, shown with a `~`).
- A turn still in progress at the end of the log is marked `Running`.

### Pulling out Bash commands

A Bash tool call is a `tool_use` block in an assistant entry:

```json
{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"ait claim demo-AbCdE.1 claude"}}
```

`block.Input.Command` reads the command. `bashCommands()` collects it from each Bash call in an entry, and the timeline also uses it as the label for a wait ("approving: ls /secret"). For other tools the label is just the tool name.

The command string is then matched by `aitCall` in `session.go` (shared with Codex). It only matches `ait claim <id>` or `ait close <id>` where a shell command starts: a line's start, or after `&&`, `||`, `;` or `|`. So `ait close a-1 && ait close b-2` records both, but `grep 'ait claim a-1' log`, or a script that searches a log for that text, records nothing. Real ait ids always contain a hyphen, which keeps `ait close --help` out. Only the command text is inspected, not whether it succeeded.

## The Codex session log

Codex keeps its logs under `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<timestamp>-<session-id>.jsonl` (`CODEX_HOME` defaults to `~/.codex`). They are filed by date, not by project, so `latestCodex` in `codex.go` reads each candidate's first line, its `session_meta`, for the project directory (`cwd`), newest first, and stops at the first match. Every line is an envelope, `{"timestamp", "type", "payload"}`. This was worked out from Codex CLI 0.159.2 logs and is just as unofficial as Claude Code's format.

| Line | What it means to `att` |
|------|------------------------|
| `type: "session_meta"` | First line. `payload.id` is the session id, `payload.cwd` the project. `payload.source` is a string (`cli`, `vscode`) for a session you started and an object for a subagent. |
| `event_msg`, `payload.type: "task_started"` | Starts a turn. |
| `event_msg`, `payload.type: "task_complete"` | Ends it. |
| `event_msg`, `item_completed` with `item.type: "UserMessage"` | The human's message; its text parts become the prompt. |
| `event_msg`, `item_completed` with `item.type: "CommandExecution"` | A shell command that ran. `item.command` is argv, such as `["/bin/bash", "-lc", "<script>"]`; the last element is checked for ait commands. |

Details that caught us out:

- Messages and commands appear twice: as `response_item` lines and as `item_completed` events. `att` only reads the `item_completed` ones. User-role `response_item` messages also include context Codex injects itself (AGENTS.md, environment), which is not the human's prompt.
- `task_complete`'s `duration_ms` includes time spent waiting on permission approvals, and nothing else in the log marks an approval. So it is not like Claude Code's `turn_duration` and is not used: every Codex turn is approximate (`Exact: false`, shown with `~`) and all of it counts as work. Time between turns is measured exactly from the task events.
- Subagents write their own session files, often newer than the parent's and with the same `cwd`. `latestCodex` skips any session whose `source` is not a string.
- A `task_started` with the previous turn still open closes that turn where its last event was. A turn still open at the end of the log is `Running`.

## Turning turns into a timeline

1. A Claude Code turn runs from the human message (or a slash command followed by agent activity) to `stop_hook_summary` or `turn_duration`. `GapBefore` is the time since the previous turn ended, which is the human reading and replying.
2. When a turn closes, `segment()` splits it into work and waits:
   - `AskUserQuestion` and `ExitPlanMode` calls are always waits ("answering a question", "reviewing a plan"), from the call to its result.
   - Any other call that took 5 seconds or more might be a permission prompt, or might just be a slow command. Longest first, each call whose duration fits inside the remaining blocked-time budget (wall clock minus `turn_duration`) is treated as a permission prompt. The rest count as work.
   - Blocked time left over that matched no tool call becomes a final "waiting on you (not matched to a tool call)" segment. It happened somewhere in the turn, but we can't tell where, so it is taken off the latest work so that work plus waits still add up to the turn.
   - Parallel tool calls that overlap an already-placed wait are skipped.
3. `issues()` credits each claimed ait issue with the active time of every turn from the one that claimed it to the one that closed it (or the last turn, if still open). Two issues open at once are both credited in full.

`Rows` in `rows.go` flattens turns into display rows (`start`, `agent`, `waiting`, `claimed`, `closed`). Bar length is a square-root scale against the longest stretch in the session, so long waits dominate but short bursts of work stay visible.

## Web server

| Route | Purpose |
|-------|---------|
| `GET /` | The embedded page |
| `GET /api/timeline` | The timeline as JSON |

The session log is resolved again on every request, so serving a directory moves on to a newer session by itself. The ETag is built from a hash of the log's path plus its modification time and size. The page polls every two seconds with `If-None-Match` and gets a 304 while the log is unchanged. Message text in the JSON is cut to 200 characters. The server listens on `127.0.0.1:8080` by default because the page shows the human's messages.

## Testing

- Standard `go test`, no helpers or dependencies
- `internal/att/codex_test.go` holds a hand-built Codex fixture (turn boundaries, injected context, duplicate command records, a running turn, ait claim and close), tests which commands count as ait calls, and tests `FindSession` against temporary Claude and Codex directories
- `internal/att/timeline_test.go` holds a hand-built Claude fixture in the log's shape. It covers a question dialog, a slash command, an image message, a sidechain claim, a permission prompt versus a slow command, and ait claim and close. Its comment explains the timings.
- `internal/web/server_test.go` checks the 304 behaviour against a temporary log file
- Run: `go test ./...`

## Local development

```bash
go build -o att ./cmd/att
./att                 # latest session for the current directory
./att --follow
./att serve
```

Releases are built by `.github/workflows/release.yml` when a `v*.*.*` tag is pushed.
