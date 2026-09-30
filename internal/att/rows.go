package att

import (
	"math"
	"time"
)

// Row kinds, in the order a session reads.
const (
	RowStart   = "start"   // the session's first message
	RowAgent   = "agent"   // a stretch of agent work
	RowWaiting = "waiting" // the agent blocked on the human
	RowClaimed = "claimed" // an ait claim
	RowClosed  = "closed"  // an ait close
)

// Row is one line of the timeline, ready for any view to draw. Working out
// what counts as a wait and what ended it happens here, once, so the
// terminal and the web page cannot disagree.
type Row struct {
	Kind string
	// At is when the row began; zero for waits, which follow on from the
	// row before.
	At time.Time
	D  time.Duration
	// Scale is the bar length for agent and waiting rows, 0 to 1: a
	// square-root scale against the session's longest stretch, so long waits
	// still clearly dominate, short bursts of work stay visible, and nothing
	// is capped.
	Scale float64
	// Text is the message for start rows and for waits that your message
	// ended (Message is true then), the reason for other waits, and the
	// issue id for ait rows.
	Text    string
	Message bool
	// Approx marks agent time taken from the wall clock because the log had
	// no turn_duration; Running marks the stretch still in progress.
	Approx, Running bool
	// Issue is set on a closed row: that issue's totals.
	Issue *Issue
}

// Rows flattens a timeline into one stream: the agent works, then waits,
// and each wait ends with whatever unblocked it - an approval, an answer, or
// your next message.
func Rows(tl Timeline) []Row {
	if len(tl.Turns) == 0 {
		return nil
	}
	issues := map[string]Issue{}
	for _, is := range tl.Issues {
		issues[is.ID] = is
	}

	rows := []Row{{Kind: RowStart, At: tl.Turns[0].Start, Text: tl.Turns[0].Prompt, Message: true}}
	for i, t := range tl.Turns {
		ait := t.Ait
		lastWork := len(t.Segments) - 1
		for lastWork > 0 && t.Segments[lastWork].Wait {
			lastWork--
		}
		for j, seg := range t.Segments {
			if seg.Wait {
				rows = append(rows, Row{Kind: RowWaiting, D: seg.D, Text: seg.Label})
				continue
			}
			last := j == lastWork
			rows = append(rows, Row{Kind: RowAgent, At: seg.Start, D: seg.D, Approx: !t.Exact, Running: t.Running && last})
			for len(ait) > 0 && (last || ait[0].At.Before(seg.Start.Add(seg.D))) {
				ev := ait[0]
				r := Row{Kind: RowClaimed, At: ev.At, Text: ev.ID}
				if ev.Action == "close" {
					r.Kind = RowClosed
					if is, ok := issues[ev.ID]; ok && is.Closed.Equal(ev.At) {
						r.Issue = &is
					}
				}
				rows = append(rows, r)
				ait = ait[1:]
			}
		}
		if i+1 < len(tl.Turns) {
			next := tl.Turns[i+1]
			rows = append(rows, Row{Kind: RowWaiting, D: next.GapBefore, Text: next.Prompt, Message: true})
		}
	}
	l := longest(tl.Turns)
	for i := range rows {
		if rows[i].Kind == RowAgent || rows[i].Kind == RowWaiting {
			rows[i].Scale = math.Sqrt(rows[i].D.Seconds() / l.Seconds())
		}
	}
	return rows
}

// longest is the longest stretch of any kind in the session, which gets the
// full bar.
func longest(turns []Turn) time.Duration {
	l := time.Second
	for _, t := range turns {
		l = max(l, t.GapBefore)
		for _, s := range t.Segments {
			l = max(l, s.D)
		}
	}
	return l
}
