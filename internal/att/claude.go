package att

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// entry is the subset of a Claude Code session log line we care about. The
// log format is Claude Code's internal one, not a documented interface, so
// everything that reads it lives in this file.
type entry struct {
	Type        string    `json:"type"`
	Subtype     string    `json:"subtype"`
	Timestamp   time.Time `json:"timestamp"`
	DurationMs  int64     `json:"durationMs"`
	IsSidechain bool      `json:"isSidechain"`
	IsMeta      bool      `json:"isMeta"`
	Origin      *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
	Name      string `json:"name"`
	Input     struct {
		Command string `json:"command"`
	} `json:"input"`
}

// summary names a tool call for the timeline: the command for Bash, the
// tool's name otherwise.
func (b block) summary() string {
	if b.Name == "Bash" && b.Input.Command != "" {
		return b.Input.Command
	}
	return b.Name
}

// text returns the message's text: the content itself when it is a plain
// string, or its text parts joined when it is a list (a message with an
// image attached, say).
func (e entry) text() (string, bool) {
	var s string
	if err := json.Unmarshal(e.Message.Content, &s); err == nil {
		return s, true
	}
	var parts []string
	for _, b := range e.blocks() {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " "), len(parts) > 0
}

func (e entry) blocks() []block {
	var bs []block
	_ = json.Unmarshal(e.Message.Content, &bs)
	return bs
}

func (e entry) isToolResult() bool {
	bs := e.blocks()
	return len(bs) > 0 && bs[0].Type == "tool_result"
}

func (e entry) isHumanPrompt() bool {
	return e.Origin != nil && e.Origin.Kind == "human"
}

// bashCommands returns the commands of any Bash tool calls in an assistant entry.
func (e entry) bashCommands() []string {
	var cmds []string
	for _, b := range e.blocks() {
		if b.Type == "tool_use" && b.Name == "Bash" {
			cmds = append(cmds, b.Input.Command)
		}
	}
	return cmds
}

// claudeTurns reads a Claude Code session log into turns, not yet segmented.
func claudeTurns(lines [][]byte) []Turn {
	var turns []Turn
	var cur *Turn
	var lastEnd, pending time.Time

	open := func(at time.Time) {
		cur = &Turn{Start: at}
		if !lastEnd.IsZero() {
			cur.GapBefore = at.Sub(lastEnd)
		}
		pending = time.Time{}
	}
	closeTurn := func(at time.Time) {
		cur.End = at
		turns = append(turns, *cur)
		lastEnd = at
		cur = nil
	}

	for _, line := range lines {
		var e entry
		if json.Unmarshal(line, &e) != nil || e.IsSidechain || e.Timestamp.IsZero() {
			continue
		}
		switch {
		case e.Type == "user" && e.isToolResult() && cur != nil:
			for _, b := range e.blocks() {
				for i := range cur.tools {
					if cur.tools[i].id == b.ToolUseID && cur.tools[i].done.IsZero() {
						cur.tools[i].done = e.Timestamp
					}
				}
			}
		case e.Type == "user" && e.isHumanPrompt():
			if cur == nil {
				open(e.Timestamp)
			}
			if s, ok := e.text(); ok && cur.Prompt == "" {
				cur.Prompt = s
			}
		case e.Type == "user" && !e.IsMeta && !e.isToolResult() && cur == nil && pending.IsZero():
			// A slash command or similar: only starts a turn if the agent
			// then does something.
			pending = e.Timestamp
		case e.Type == "assistant":
			if cur == nil {
				if pending.IsZero() {
					continue
				}
				open(pending)
			}
			cur.End = e.Timestamp
			for _, b := range e.blocks() {
				if b.Type == "tool_use" {
					cur.tools = append(cur.tools, toolCall{id: b.ID, name: b.Name, summary: b.summary(), at: e.Timestamp})
				}
			}
			for _, cmd := range e.bashCommands() {
				cur.Ait = append(cur.Ait, aitEvents(cmd, e.Timestamp)...)
			}
		case e.Type == "system" && e.Subtype == "stop_hook_summary" && cur != nil:
			// Stop hooks fire as the turn ends; turn_duration, if any,
			// follows with the exact figure.
			cur.Active, cur.Exact = e.Timestamp.Sub(cur.Start), false
			closeTurn(e.Timestamp)
		case e.Type == "system" && e.Subtype == "turn_duration":
			d := time.Duration(e.DurationMs) * time.Millisecond
			if cur != nil {
				cur.Active, cur.Exact = d, true
				closeTurn(e.Timestamp)
			} else if n := len(turns); n > 0 && !turns[n-1].Exact {
				turns[n-1].Active, turns[n-1].Exact = d, true
			}
		}
	}
	if cur != nil {
		cur.Running = true
		if cur.End.IsZero() {
			cur.End = cur.Start
		}
		turns = append(turns, *cur)
	}
	return turns
}

// latestClaude returns Claude Code's most recently modified session log for
// a project, or "" if there is none. Claude Code keeps them in a directory
// named after the project's absolute path, with every non-alphanumeric
// character turned into '-'.
func latestClaude(home, projectDir string) (string, time.Time) {
	name := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(projectDir, "-")
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", name, "*.jsonl"))
	var best string
	var bestMod time.Time
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if info.ModTime().After(bestMod) {
			best, bestMod = m, info.ModTime()
		}
	}
	return best, bestMod
}
