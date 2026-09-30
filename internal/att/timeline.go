package att

import (
	"sort"
	"strings"
	"time"
)

// Turn is one prompt-to-reply exchange.
type Turn struct {
	Start, End time.Time
	// Active is Claude Code's own turn_duration, which already leaves out
	// time spent on question dialogs and permission prompts.
	Active time.Duration
	// Exact is false when the log had no such figure (a Claude Code turn
	// with no turn_duration, or any Codex turn) and Active is the wall clock
	// less any waits found.
	Exact bool
	// Running is true for a turn still in progress at the end of the log.
	Running bool
	// GapBefore is the time between the previous turn ending and this one
	// starting: the human reading and replying.
	GapBefore time.Duration
	Prompt    string
	Ait       []AitEvent
	// Segments splits the turn, in order, into agent work and waits on the
	// human. Filled in when the turn ends.
	Segments []Segment

	tools []toolCall
}

// Segment is a stretch of a turn: the agent working, or the agent blocked
// on the human (Wait) for the reason in Label.
type Segment struct {
	Start time.Time
	D     time.Duration
	Wait  bool
	Label string
}

type toolCall struct {
	id, name, summary string
	at, done          time.Time
}

// BlockedWait is time inside the turn spent waiting on the human.
func (t Turn) BlockedWait() time.Duration {
	var d time.Duration
	for _, s := range t.Segments {
		if s.Wait {
			d += s.D
		}
	}
	return d
}

// dialogs are tools that are really questions to the human.
var dialogs = map[string]string{
	"AskUserQuestion": "answering a question",
	"ExitPlanMode":    "reviewing a plan",
}

// minWait keeps tiny timing noise out of the waits.
const minWait = 5 * time.Second

// segment splits a finished turn into agent work and waits. Dialog tools are
// always waits. For any other slow tool call, the leftover between the wall
// clock and turn_duration (time Claude Code already knows it was blocked)
// decides: a gap that fits inside it was a permission prompt, one that does
// not was a slow command, and so work.
func (t *Turn) segment() {
	var budget time.Duration
	if t.Exact {
		budget = t.End.Sub(t.Start) - t.Active
	}

	var waits []Segment
	var others []toolCall
	for _, c := range t.tools {
		if c.done.IsZero() {
			continue
		}
		gap := c.done.Sub(c.at)
		if label, ok := dialogs[c.name]; ok {
			waits = append(waits, Segment{Start: c.at, D: gap, Wait: true, Label: label})
			budget -= gap
		} else if gap >= minWait {
			others = append(others, c)
		}
	}
	sort.SliceStable(others, func(i, j int) bool { return others[i].done.Sub(others[i].at) > others[j].done.Sub(others[j].at) })
	for _, c := range others {
		if gap := c.done.Sub(c.at); gap <= budget+minWait {
			waits = append(waits, Segment{Start: c.at, D: gap, Wait: true, Label: "approving: " + c.summary})
			budget -= gap
		}
	}
	sort.SliceStable(waits, func(i, j int) bool { return waits[i].Start.Before(waits[j].Start) })

	cursor := t.Start
	for _, w := range waits {
		if w.Start.Before(cursor) {
			continue // overlaps a wait already placed (parallel tool calls)
		}
		if d := w.Start.Sub(cursor); d > 0 {
			t.Segments = append(t.Segments, Segment{Start: cursor, D: d})
		}
		t.Segments = append(t.Segments, w)
		cursor = w.Start.Add(w.D)
	}
	if d := t.End.Sub(cursor); d > 0 || len(t.Segments) == 0 {
		t.Segments = append(t.Segments, Segment{Start: cursor, D: max(d, 0)})
	}
	if t.Exact {
		if left := t.End.Sub(t.Start) - t.Active - t.BlockedWait(); left > minWait {
			t.trimWork(left)
			t.Segments = append(t.Segments, Segment{Start: t.End.Add(-left), D: left, Wait: true, Label: "waiting on you (not matched to a tool call)"})
		}
	}
	if !t.Exact {
		t.Active = t.End.Sub(t.Start) - t.BlockedWait()
	}
}

// trimWork takes d off the turn's work, latest first, for blocked time that
// happened somewhere in the turn but matched no tool call. Work trimmed to
// nothing is dropped.
func (t *Turn) trimWork(d time.Duration) {
	kept := t.Segments[:0]
	for i := len(t.Segments) - 1; i >= 0; i-- {
		if s := &t.Segments[i]; !s.Wait {
			cut := min(s.D, d)
			s.D -= cut
			d -= cut
		}
	}
	for _, s := range t.Segments {
		if s.Wait || s.D > 0 {
			kept = append(kept, s)
		}
	}
	t.Segments = kept
}

type AitEvent struct {
	At     time.Time
	Action string // "claim" or "close"
	ID     string
}

type Issue struct {
	ID              string
	Claimed, Closed time.Time // Closed is zero while still open
	// Active and Wall both cover every turn from the claiming one to the
	// closing one, so they compare like with like.
	Active, Wall time.Duration
}

type Timeline struct {
	// Agent names the tool that wrote the log, and ID is the session's id.
	Agent, ID string
	Turns     []Turn
	Issues    []Issue
}

// ShortID is the start of the session id, enough to tell sessions apart.
func (tl Timeline) ShortID() string { return tl.ID[:min(8, len(tl.ID))] }

func (tl Timeline) Totals() (active, blocked, between, wall time.Duration) {
	for _, t := range tl.Turns {
		active += t.Active
		blocked += t.BlockedWait()
		between += t.GapBefore
	}
	if n := len(tl.Turns); n > 0 {
		wall = tl.Turns[n-1].End.Sub(tl.Turns[0].Start)
	}
	return
}

// issues credits each claimed issue with the active time of every turn from
// the one that claimed it to the one that closed it. Two issues open at once
// are both credited in full.
func issues(turns []Turn) []Issue {
	type span struct {
		issue      Issue
		first, end int
	}
	spans := map[string]*span{}
	for i, t := range turns {
		for _, ev := range t.Ait {
			s, ok := spans[ev.ID]
			if ev.Action == "claim" && !ok {
				spans[ev.ID] = &span{issue: Issue{ID: ev.ID, Claimed: ev.At}, first: i, end: len(turns) - 1}
			}
			if ev.Action == "close" && ok && s.issue.Closed.IsZero() {
				s.issue.Closed, s.end = ev.At, i
			}
		}
	}
	var out []Issue
	for _, s := range spans {
		for i := s.first; i <= s.end; i++ {
			s.issue.Active += turns[i].Active
		}
		s.issue.Wall = turns[s.end].End.Sub(turns[s.first].Start)
		out = append(out, s.issue)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Claimed.Equal(out[j].Claimed) {
			return out[i].Claimed.Before(out[j].Claimed)
		}
		return strings.Compare(out[i].ID, out[j].ID) < 0
	})
	return out
}
