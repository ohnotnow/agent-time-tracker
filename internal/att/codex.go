package att

import (
	"bufio"
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// codexLine is the subset of a Codex rollout log line we care about. Like
// Claude Code's, the format is internal and undocumented; everything that
// reads it lives in this file.
type codexLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		// ID, Cwd and Source are set on session_meta. Source is a string
		// ("cli", "vscode") for a session you started, and an object for a
		// subagent.
		ID     string          `json:"id"`
		Cwd    string          `json:"cwd"`
		Source json.RawMessage `json:"source"`
		Item   struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
	} `json:"payload"`
}

func (l codexLine) isSessionMeta() bool { return l.Type == "session_meta" }

func (l codexLine) isSubagent() bool {
	var s string
	return json.Unmarshal(l.Payload.Source, &s) != nil
}

// codexTurns reads a Codex rollout log into turns, not yet segmented, and
// returns the session id from its metadata.
//
// Codex logs explicit task_started and task_complete events, so turn
// boundaries are exact. Its duration_ms includes time spent waiting on
// approvals, which Codex does not otherwise log, so it is not used: every
// turn is approximate (Exact false) and all of it counts as work.
func codexTurns(lines [][]byte) (string, []Turn) {
	var id string
	var turns []Turn
	var cur *Turn
	var lastEnd time.Time

	closeTurn := func() {
		turns = append(turns, *cur)
		lastEnd = cur.End
		cur = nil
	}

	for _, line := range lines {
		var l codexLine
		if json.Unmarshal(line, &l) != nil || l.Timestamp.IsZero() {
			continue
		}
		if l.isSessionMeta() {
			id = l.Payload.ID
			continue
		}
		if l.Type != "event_msg" {
			continue
		}
		switch l.Payload.Type {
		case "task_started":
			if cur != nil {
				closeTurn() // the previous turn never logged task_complete
			}
			cur = &Turn{Start: l.Timestamp, End: l.Timestamp}
			if !lastEnd.IsZero() {
				cur.GapBefore = l.Timestamp.Sub(lastEnd)
			}
		case "task_complete":
			if cur != nil {
				cur.End = l.Timestamp
				closeTurn()
			}
		case "item_completed":
			if cur == nil {
				continue
			}
			cur.End = l.Timestamp
			// The same message and command also appear as response_item
			// lines, and user-role response_items include context Codex
			// injects itself; the completed items are the ones to trust.
			switch l.Payload.Item.Type {
			case "UserMessage":
				if cur.Prompt == "" {
					var parts []string
					for _, c := range l.Payload.Item.Content {
						if c.Type == "text" {
							parts = append(parts, c.Text)
						}
					}
					cur.Prompt = strings.Join(parts, " ")
				}
			case "CommandExecution":
				// The command is argv, such as ["/bin/bash", "-lc", script];
				// the script is the part a person would have typed.
				if cmd := l.Payload.Item.Command; len(cmd) > 0 {
					cur.Ait = append(cur.Ait, aitEvents(cmd[len(cmd)-1], l.Timestamp)...)
				}
			}
		}
	}
	if cur != nil {
		cur.Running = true
		turns = append(turns, *cur)
	}
	return id, turns
}

// latestCodex returns the most recently modified Codex session for a
// project that is newer than after, or "" if there is none. Codex files
// sessions by date under $CODEX_HOME (default ~/.codex), so the project
// comes from each file's first line, its session_meta. Subagent sessions
// are skipped. Files are checked newest first, and only those newer than
// after, so the usual case reads one or two first lines.
func latestCodex(home, projectDir string, after time.Time) string {
	root := filepath.Join(cmp.Or(os.Getenv("CODEX_HOME"), filepath.Join(home, ".codex")), "sessions")
	type candidate struct {
		path string
		mod  time.Time
	}
	var cands []candidate
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(after) {
			cands = append(cands, candidate{path, info.ModTime()})
		}
		return nil
	})
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	for _, c := range cands {
		if meta, ok := codexMeta(c.path); ok && meta.Payload.Cwd == projectDir && !meta.isSubagent() {
			return c.path
		}
	}
	return ""
}

// codexMeta reads the session_meta line a Codex log starts with.
func codexMeta(path string) (codexLine, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexLine{}, false
	}
	defer f.Close()
	first, _ := bufio.NewReader(f).ReadBytes('\n')
	var l codexLine
	return l, json.Unmarshal(first, &l) == nil && l.isSessionMeta()
}
