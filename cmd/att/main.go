// Command att shows how much of a Claude Code session the agent spent
// working and how much it spent waiting on you.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: att [session.jsonl | project-dir]\n\nWith no argument, reads the latest Claude Code session for the current directory.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	path, err := sessionPath(fs.Arg(0))
	if err != nil {
		return err
	}
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
	title := "session " + id[:min(8, len(id))]
	att.Render(os.Stdout, tl, title, useColour())
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
