package att

import (
	"strings"
	"testing"
	"time"
)

// A trimmed-down session in Claude Code's log shape. Turn one waits 5 minutes
// on a question. Turn two (started by a message with an image attached)
// claims and closes an issue, sits 2 minutes on a permission prompt, and runs
// a genuinely slow 1-minute command. turn_duration says 100s of work, so the
// 2-minute gap fits the blocked time and the 1-minute one does not.
const fixture = `
{"type":"user","timestamp":"2026-09-30T10:00:00Z","origin":{"kind":"human"},"message":{"content":"Please look at the flux capacitor"}}
{"type":"assistant","timestamp":"2026-09-30T10:00:05Z","message":{"content":[{"type":"tool_use","id":"t1","name":"AskUserQuestion"}]}}
{"type":"user","timestamp":"2026-09-30T10:05:05Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}
{"type":"assistant","timestamp":"2026-09-30T10:05:10Z","message":{"content":[{"type":"text","text":"Done"}]}}
{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-09-30T10:05:10Z"}
{"type":"system","subtype":"turn_duration","durationMs":10000,"timestamp":"2026-09-30T10:05:10Z"}
{"type":"user","timestamp":"2026-09-30T10:06:00Z","message":{"content":"<command-name>/permissions</command-name>"}}
{"type":"user","timestamp":"2026-09-30T10:06:00Z","isMeta":true,"message":{"content":"<local-command-caveat>"}}
{"type":"user","timestamp":"2026-09-30T10:07:10Z","origin":{"kind":"human"},"message":{"content":[{"type":"text","text":"Go ahead"},{"type":"image"}]}}
{"type":"assistant","timestamp":"2026-09-30T10:07:20Z","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"ait claim demo-AbCdE.1 claude"}}]}}
{"type":"user","timestamp":"2026-09-30T10:07:21Z","message":{"content":[{"type":"tool_result","tool_use_id":"t2"}]}}
{"type":"assistant","timestamp":"2026-09-30T10:07:12Z","isSidechain":true,"message":{"content":[{"type":"tool_use","id":"s1","name":"Bash","input":{"command":"ait claim demo-Other claude"}}]}}
{"type":"assistant","timestamp":"2026-09-30T10:07:30Z","message":{"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"ls /secret"}}]}}
{"type":"user","timestamp":"2026-09-30T10:09:30Z","message":{"content":[{"type":"tool_result","tool_use_id":"t3"}]}}
{"type":"assistant","timestamp":"2026-09-30T10:09:40Z","message":{"content":[{"type":"tool_use","id":"t4","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","timestamp":"2026-09-30T10:10:40Z","message":{"content":[{"type":"tool_result","tool_use_id":"t4"}]}}
{"type":"assistant","timestamp":"2026-09-30T10:10:50Z","message":{"content":[{"type":"tool_use","id":"t5","name":"Bash","input":{"command":"ait close demo-AbCdE.1 && ait close --help"}}]}}
{"type":"system","subtype":"turn_duration","durationMs":100000,"timestamp":"2026-09-30T10:10:50Z"}
`

type seg struct {
	d     time.Duration
	wait  bool
	label string
}

func segs(t Turn) []seg {
	var out []seg
	for _, s := range t.Segments {
		out = append(out, seg{s.D, s.Wait, s.Label})
	}
	return out
}

func equalSegs(a, b []seg) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuild(t *testing.T) {
	tl, err := Build(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(tl.Turns))
	}
	first, second := tl.Turns[0], tl.Turns[1]

	t.Run("a question splits the turn where it happened", func(t *testing.T) {
		want := []seg{{5 * time.Second, false, ""}, {5 * time.Minute, true, "answering a question"}, {5 * time.Second, false, ""}}
		if got := segs(first); !equalSegs(got, want) {
			t.Errorf("segments = %+v, want %+v", got, want)
		}
	})

	t.Run("a slash command does not start the turn, the message does", func(t *testing.T) {
		if !second.Start.Equal(time.Date(2026, 9, 30, 10, 7, 10, 0, time.UTC)) {
			t.Errorf("start = %v, want the human message at 10:07:10", second.Start)
		}
		if second.GapBefore != 2*time.Minute {
			t.Errorf("gap = %v, want 2m", second.GapBefore)
		}
		if second.Prompt != "Go ahead" {
			t.Errorf("prompt = %q, want the text part of the message", second.Prompt)
		}
	})

	t.Run("a slow call that fits the blocked time is a permission prompt, one that does not is work", func(t *testing.T) {
		want := []seg{{20 * time.Second, false, ""}, {2 * time.Minute, true, "approving: ls /secret"}, {80 * time.Second, false, ""}}
		if got := segs(second); !equalSegs(got, want) {
			t.Errorf("segments = %+v, want %+v", got, want)
		}
	})

	t.Run("ait claim and close, ignoring sidechains and flags", func(t *testing.T) {
		if len(tl.Issues) != 1 {
			t.Fatalf("got issues %+v, want just demo-AbCdE.1", tl.Issues)
		}
		is := tl.Issues[0]
		if is.ID != "demo-AbCdE.1" || is.Closed.IsZero() || is.Active != 100*time.Second {
			t.Errorf("issue = %+v", is)
		}
	})
}

func TestBuildWithoutTurnDurationFallsBackToWallClock(t *testing.T) {
	const log = `
{"type":"user","timestamp":"2026-09-30T10:00:00Z","origin":{"kind":"human"},"message":{"content":"hi"}}
{"type":"assistant","timestamp":"2026-09-30T10:00:30Z","message":{"content":[{"type":"text","text":"hello"}]}}
{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-09-30T10:00:30Z"}
`
	tl, err := Build(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if got := tl.Turns[0]; got.Exact || got.Active != 30*time.Second {
		t.Errorf("turn = %+v, want approximate 30s", got)
	}
}

func TestDur(t *testing.T) {
	for d, want := range map[time.Duration]string{
		13 * time.Second:                "13s",
		5*time.Minute + 37*time.Second:  "5m37s",
		time.Hour + 2*time.Minute + 5e9: "1h02m",
	} {
		if got := Dur(d); got != want {
			t.Errorf("Dur(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRows(t *testing.T) {
	tl, err := Build(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	rows := Rows(tl)

	var kinds []string
	for _, r := range rows {
		kinds = append(kinds, r.Kind)
	}
	want := "start agent waiting agent waiting agent claimed waiting agent closed"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("kinds = %s\nwant    %s", got, want)
	}

	t.Run("the wait between turns ends with your next message", func(t *testing.T) {
		if r := rows[4]; !r.Message || r.Text != "Go ahead" || r.D != 2*time.Minute {
			t.Errorf("row = %+v", r)
		}
	})
	t.Run("a mid-turn wait carries its reason, not a message", func(t *testing.T) {
		if r := rows[7]; r.Message || r.Text != "approving: ls /secret" {
			t.Errorf("row = %+v", r)
		}
	})
	t.Run("the close row carries the issue's totals", func(t *testing.T) {
		if r := rows[9]; r.Issue == nil || r.Issue.Active != 100*time.Second {
			t.Errorf("row = %+v", r)
		}
	})
}
