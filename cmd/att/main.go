// Command att shows how much of a Claude Code or Codex session the agent
// spent working and how much it spent waiting on you.
package main

import (
	"cmp"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohnotnow/agent-time-tracker/internal/att"
	"github.com/ohnotnow/agent-time-tracker/internal/web"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "att:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "serve" {
		return serve(args[1:])
	}
	fs := flag.NewFlagSet("att", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "keep watching, redrawing whenever the session log changes")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: att [--follow] [session.jsonl | project-dir]\n       att serve [--listen addr] [session.jsonl | project-dir]\n\nWith no session or directory, reads the latest Claude Code or Codex session for the current directory.")
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

// serve runs the live web page for a session or directory.
func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "address to listen on; the page shows your messages, so it stays on localhost unless you say otherwise")
	if err := fs.Parse(args); err != nil {
		return err
	}
	arg := fs.Arg(0)
	if _, err := sessionPath(arg); err != nil {
		return err
	}

	project := filepath.Base(arg)
	if !strings.HasSuffix(arg, ".jsonl") {
		abs, err := filepath.Abs(cmp.Or(arg, "."))
		if err != nil {
			return err
		}
		project = filepath.Base(abs)
	}

	srv := &web.Server{Project: project, Resolve: func() (string, error) { return sessionPath(arg) }}
	fmt.Printf("Serving %s on http://%s - Ctrl-C to stop\n", project, *listen)
	return http.ListenAndServe(*listen, srv)
}

// show builds the timeline for one session log and draws it.
func show(path string, colour bool, suffix string) error {
	tl, err := att.Load(path)
	if err != nil {
		return err
	}
	title := tl.Agent + " session " + tl.ShortID() + suffix
	att.Render(os.Stdout, tl, title, colour)
	return nil
}

// sessionPath resolves the argument to a session log: a .jsonl file is used
// as is, a directory (default: the current one) means its latest session
// from either agent.
func sessionPath(arg string) (string, error) {
	if strings.HasSuffix(arg, ".jsonl") {
		return arg, nil
	}
	if arg == "" {
		arg = "."
	}
	return att.FindSession(arg)
}

func useColour() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
