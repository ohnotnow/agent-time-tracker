// Package web serves the timeline as a live page: the same rows the terminal
// draws, handed to an embedded index.html as JSON.
package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohnotnow/agent-time-tracker/internal/att"
)

//go:embed static/index.html
var indexHTML []byte

// textLimit keeps a long pasted message from bloating every poll.
const textLimit = 200

type Server struct {
	// Project names what is being watched, for the page header.
	Project string
	// Resolve returns the session log to show. It is called on every
	// request, so a directory can move on to a newer session.
	Resolve func() (string, error)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	case r.URL.Path == "/api/timeline" && r.Method == http.MethodGet:
		s.handleTimeline(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleTimeline answers 304 while the log is unchanged, so the page can
// poll cheaply and only redraw when there is something new.
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	path, err := s.Resolve()
	if err != nil {
		jsonError(w, http.StatusNotFound, err)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		jsonError(w, http.StatusNotFound, err)
		return
	}
	h := fnv.New64a()
	h.Write([]byte(path))
	etag := fmt.Sprintf(`"%x-%x-%x"`, h.Sum64(), info.ModTime().UnixNano(), info.Size())
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	tl, err := att.Build(f)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload(s.Project, path, tl))
}

type apiTotal struct {
	Seconds float64 `json:"seconds"`
	Dur     string  `json:"dur"`
}

func total(d time.Duration) apiTotal { return apiTotal{d.Seconds(), att.Dur(d)} }

type apiIssue struct {
	ID      string     `json:"id"`
	Claimed time.Time  `json:"claimed"`
	Closed  *time.Time `json:"closed"`
	Agent   string     `json:"agent"`
	Wall    string     `json:"wall"`
}

type apiRow struct {
	Kind    string     `json:"kind"`
	At      *time.Time `json:"at"`
	Seconds float64    `json:"seconds"`
	Dur     string     `json:"dur"`
	Scale   float64    `json:"scale"`
	Text    string     `json:"text"`
	Message bool       `json:"message"`
	Approx  bool       `json:"approx"`
	Running bool       `json:"running"`
	Issue   *apiIssue  `json:"issue,omitempty"`
}

type apiTimeline struct {
	Project string `json:"project"`
	Session string `json:"session"`
	Totals  struct {
		Agent   apiTotal `json:"agent"`
		MidTurn apiTotal `json:"midTurn"`
		Between apiTotal `json:"between"`
		Waiting apiTotal `json:"waiting"`
		Wall    apiTotal `json:"wall"`
	} `json:"totals"`
	Rows   []apiRow   `json:"rows"`
	Issues []apiIssue `json:"issues"`
}

func issue(is att.Issue) apiIssue {
	out := apiIssue{ID: is.ID, Claimed: is.Claimed, Agent: att.Dur(is.Active), Wall: att.Dur(is.Wall)}
	if !is.Closed.IsZero() {
		out.Closed = &is.Closed
	}
	return out
}

func payload(project, path string, tl att.Timeline) apiTimeline {
	out := apiTimeline{Project: project, Rows: []apiRow{}, Issues: []apiIssue{}}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	out.Session = id[:min(8, len(id))]

	active, blocked, between, wall := tl.Totals()
	out.Totals.Agent = total(active)
	out.Totals.MidTurn = total(blocked)
	out.Totals.Between = total(between)
	out.Totals.Waiting = total(blocked + between)
	out.Totals.Wall = total(wall)

	for _, r := range att.Rows(tl) {
		row := apiRow{Kind: r.Kind, Seconds: r.D.Seconds(), Dur: att.Dur(r.D), Scale: r.Scale,
			Text: att.Snippet(r.Text, textLimit), Message: r.Message, Approx: r.Approx, Running: r.Running}
		if !r.At.IsZero() {
			at := r.At
			row.At = &at
		}
		if r.Issue != nil {
			is := issue(*r.Issue)
			row.Issue = &is
		}
		out.Rows = append(out.Rows, row)
	}
	for _, is := range tl.Issues {
		out.Issues = append(out.Issues, issue(is))
	}
	return out
}

func jsonError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
