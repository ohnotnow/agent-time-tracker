// Command att shows how much of a Claude Code session the agent spent
// working and how much it spent waiting on you.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohnotnow/agent-time-tracker/internal/att"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "att:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("att", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "keep watching, redrawing whenever the session log changes")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: att [--follow] [session.jsonl | project-dir]\n\nWith no argument, reads the latest Claude Code session for the current directory.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	colour := useColour()

	if !*follow {
		path, err := sessionPath(fs.Arg(0))
		if err != nil {
			return err
		}
		return show(path, colour, "")
	}

	// Re-resolve the path on every poll, so following a directory moves on
	// to a new session when one starts; a named .jsonl file stays put.
	var lastPath string
	var lastMod time.Time
	var lastSize int64
	for ; ; time.Sleep(time.Second) {
		path, err := sessionPath(fs.Arg(0))
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if path == lastPath && info.ModTime().Equal(lastMod) && info.Size() == lastSize {
			continue
		}
		lastPath, lastMod, lastSize = path, info.ModTime(), info.Size()
		if colour {
			fmt.Print("\033[H\033[2J")
		}
		if err := show(path, colour, "  -  following, Ctrl-C to stop"); err != nil {
			return err
		}
	}
}

// show builds the timeline for one session log and draws it.
func show(path string, colour bool, suffix string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	tl, err := att.Build(f)
	if err != nil {
		return err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	title := "session " + id[:min(8, len(id))] + suffix
	att.Render(os.Stdout, tl, title, colour)
	return nil
}

// sessionPath resolves the argument to a session log: a .jsonl file is used
// as is, a directory (default: the current one) means its latest session.
func sessionPath(arg string) (string, error) {
	if strings.HasSuffix(arg, ".jsonl") {
		return arg, nil
	}
	if arg == "" {
		arg = "."
	}
	dir, err := att.ProjectLogDir(arg)
	if err != nil {
		return "", err
	}
	return att.LatestSession(dir)
}

func useColour() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
