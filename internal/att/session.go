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

// Session logs come from Claude Code (claude.go) or Codex (codex.go). Each
// reader turns its log into turns; everything after that is shared.

// Load builds the timeline for the session log at path.
func Load(path string) (Timeline, error) {
	f, err := os.Open(path)
	if err != nil {
		return Timeline{}, err
	}
	defer f.Close()
	tl, err := Build(f)
	if tl.ID == "" {
		tl.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	return tl, err
}

func Build(r io.Reader) (Timeline, error) {
	lines, err := readLines(r)
	if err != nil {
		return Timeline{}, err
	}
	var tl Timeline
	if isCodex(lines) {
		tl.Agent = "Codex"
		tl.ID, tl.Turns = codexTurns(lines)
	} else {
		tl.Agent = "Claude Code"
		tl.Turns = claudeTurns(lines)
	}
	for i := range tl.Turns {
		tl.Turns[i].segment()
	}
	tl.Issues = issues(tl.Turns)
	return tl, nil
}

// isCodex reports whether a log is Codex's: its first line is session_meta.
func isCodex(lines [][]byte) bool {
	var l codexLine
	return len(lines) > 0 && json.Unmarshal(lines[0], &l) == nil && l.isSessionMeta()
}

// readLines returns the log's non-blank lines. A line still being written
// is included and simply fails to parse until it is finished.
func readLines(r io.Reader) ([][]byte, error) {
	var out [][]byte
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			out = append(out, line)
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// aitCall matches "ait claim <id>" and "ait close <id>" where a shell command
// starts: a line's start, or after &&, ||, ; or |. That keeps out mentions
// inside quotes, such as a script searching a log for them. Real ids always
// contain a hyphen (prefix-XXXXX), which keeps "ait close --help" out.
var aitCall = regexp.MustCompile(`(?m)(?:^|&&|\|\||[;|])\s*ait\s+(claim|close)\s+([A-Za-z0-9]+-[A-Za-z0-9.]+)`)

// aitEvents finds every ait claim and close in a shell command.
func aitEvents(cmd string, at time.Time) []AitEvent {
	var evs []AitEvent
	for _, m := range aitCall.FindAllStringSubmatch(cmd, -1) {
		evs = append(evs, AitEvent{At: at, Action: m[1], ID: m[2]})
	}
	return evs
}

// FindSession returns the newest session log for a project directory,
// whichever agent wrote it.
func FindSession(projectDir string) (string, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	claude, mod := latestClaude(home, abs)
	if codex := latestCodex(home, abs, mod); codex != "" {
		return codex, nil
	}
	if claude == "" {
		return "", fmt.Errorf("no Claude Code or Codex sessions for %s", abs)
	}
	return claude, nil
}
