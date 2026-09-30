package att

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A trimmed-down session in Codex's rollout log shape. Turn one claims an
// issue, with injected context and a tool wrapper that both mention it
// alongside the real command. Turn two, five minutes later, closes it.
// Turn three is still running.
const codexFixture = `{"timestamp":"2026-09-30T09:59:00Z","type":"session_meta","payload":{"id":"01a0f39f-demo","cwd":"/tmp/demo","source":"cli"}}
{"timestamp":"2026-09-30T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"a"}}
{"timestamp":"2026-09-30T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context> ait claim demo-Injected codex"}]}}
{"timestamp":"2026-09-30T10:00:01Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"a","item":{"type":"UserMessage","content":[{"type":"text","text":"Please look at the flux capacitor"}]}}}
{"timestamp":"2026-09-30T10:00:19Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"c1","input":"tools.exec_command({cmd:\"ait claim demo-AbCdE.1 codex\"})"}}
{"timestamp":"2026-09-30T10:00:20Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"a","item":{"type":"CommandExecution","command":["/bin/bash","-lc","ait claim demo-AbCdE.1 codex"],"exit_code":0}}}
{"timestamp":"2026-09-30T10:03:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"a","duration_ms":180000}}
{"timestamp":"2026-09-30T10:08:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"b"}}
{"timestamp":"2026-09-30T10:08:00Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"b","item":{"type":"UserMessage","content":[{"type":"text","text":"Go ahead"}]}}}
{"timestamp":"2026-09-30T10:09:00Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"b","item":{"type":"CommandExecution","command":["/bin/bash","-lc","ait close demo-AbCdE.1"],"exit_code":0}}}
{"timestamp":"2026-09-30T10:10:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"b","duration_ms":120000}}
{"timestamp":"2026-09-30T10:12:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"c"}}
{"timestamp":"2026-09-30T10:12:30Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"c","item":{"type":"AgentMessage"}}}
`

func TestBuildCodex(t *testing.T) {
	tl, err := Build(strings.NewReader(codexFixture))
	if err != nil {
		t.Fatal(err)
	}
	if tl.Agent != "Codex" || tl.ID != "01a0f39f-demo" {
		t.Errorf("agent, id = %q, %q", tl.Agent, tl.ID)
	}
	if len(tl.Turns) != 3 {
		t.Fatalf("got %d turns, want 3", len(tl.Turns))
	}
	first, second, third := tl.Turns[0], tl.Turns[1], tl.Turns[2]

	t.Run("turns run from task_started to task_complete, approximately", func(t *testing.T) {
		if first.Active != 3*time.Minute || first.Exact || first.BlockedWait() != 0 {
			t.Errorf("first = %+v", first)
		}
		if second.GapBefore != 5*time.Minute {
			t.Errorf("gap = %v, want 5m", second.GapBefore)
		}
	})
	t.Run("the prompt is the user's message, not injected context", func(t *testing.T) {
		if first.Prompt != "Please look at the flux capacitor" {
			t.Errorf("prompt = %q", first.Prompt)
		}
	})
	t.Run("a turn with no task_complete is running", func(t *testing.T) {
		if !third.Running || third.Active != 30*time.Second {
			t.Errorf("third = %+v", third)
		}
	})
	t.Run("ait comes from executed commands only, once each", func(t *testing.T) {
		if len(first.Ait) != 1 || len(second.Ait) != 1 {
			t.Fatalf("ait = %+v, %+v", first.Ait, second.Ait)
		}
		if len(tl.Issues) != 1 || tl.Issues[0].ID != "demo-AbCdE.1" || tl.Issues[0].Active != 5*time.Minute {
			t.Errorf("issues = %+v", tl.Issues)
		}
	})
}

func TestFindSessionPicksTheNewestFromEitherAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	project := filepath.Join(home, "project")
	now := time.Now()

	write := func(path, content string, age time.Duration) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return path
	}
	meta := func(cwd, source string) string {
		return `{"timestamp":"2026-09-30T10:00:00Z","type":"session_meta","payload":{"id":"x","cwd":"` + cwd + `","source":` + source + `}}` + "\n"
	}
	sessions := filepath.Join(home, "codex", "sessions", "2026", "09", "30")
	codex := write(filepath.Join(sessions, "rollout-root.jsonl"), meta(project, `"cli"`), 2*time.Hour)
	write(filepath.Join(sessions, "rollout-subagent.jsonl"), meta(project, `{"subagent":{"other":"guardian"}}`), 0)
	write(filepath.Join(sessions, "rollout-elsewhere.jsonl"), meta(filepath.Join(home, "other"), `"cli"`), time.Minute)
	claudeDir := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(project, "-")
	claude := write(filepath.Join(home, ".claude", "projects", claudeDir, "s.jsonl"), "", 3*time.Hour)

	t.Run("a newer Codex session wins, skipping subagents and other projects", func(t *testing.T) {
		if got, err := FindSession(project); err != nil || got != codex {
			t.Errorf("got %q, %v, want %q", got, err, codex)
		}
	})
	t.Run("a newer Claude Code session wins", func(t *testing.T) {
		os.Chtimes(claude, now.Add(-time.Hour), now.Add(-time.Hour))
		if got, err := FindSession(project); err != nil || got != claude {
			t.Errorf("got %q, %v, want %q", got, err, claude)
		}
	})
}

func TestAitEventsOnlyCountsCommandsNotMentions(t *testing.T) {
	for cmd, want := range map[string]int{
		"ait claim a-1 claude":                              1,
		"cd x && ait close a-1":                             1,
		"ait close a-1 && ait close b-2":                    2,
		"cd x\nait close a-1":                               1,
		"ait close --help":                                  0,
		"grep 'ait claim a-1' log":                          0,
		"python3 - <<'PY'\nprint('ait claim a-1' in s)\nPY": 0,
	} {
		if got := len(aitEvents(cmd, time.Time{})); got != want {
			t.Errorf("%q: got %d events, want %d", cmd, got, want)
		}
	}
}
