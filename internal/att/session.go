package att

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// aitCall matches "ait claim <id>" and "ait close <id>". Real ids always
// contain a hyphen (prefix-XXXXX), which keeps "ait close --help" out.
var aitCall = regexp.MustCompile(`\bait\s+(claim|close)\s+([A-Za-z0-9]+-[A-Za-z0-9.]+)`)

func readEntries(r io.Reader) ([]entry, error) {
	var out []entry
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var e entry
			if jerr := json.Unmarshal(line, &e); jerr == nil {
				out = append(out, e)
			}
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// ProjectLogDir is where Claude Code keeps session logs for a project
// directory: every non-alphanumeric character of the absolute path becomes '-'.
func ProjectLogDir(projectDir string) (string, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(abs, "-")
	return filepath.Join(home, ".claude", "projects", name), nil
}

// LatestSession returns the most recently modified session log in dir.
func LatestSession(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return "", err
	}
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
	if best == "" {
		return "", fmt.Errorf("no session logs in %s", dir)
	}
	return best, nil
}
