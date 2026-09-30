package att

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

const barWidth = 20

type palette struct{ agent, you, ait, dim, reset string }

func newPalette(colour bool) palette {
	if !colour {
		return palette{}
	}
	return palette{agent: "\033[32m", you: "\033[33m", ait: "\033[36m", dim: "\033[2m", reset: "\033[0m"}
}

// Render writes the timeline as a plain terminal report.
func Render(w io.Writer, tl Timeline, title string, colour bool) {
	p := newPalette(colour)
	if len(tl.Turns) == 0 {
		fmt.Fprintln(w, "No turns found in this session.")
		return
	}

	scale := longest(tl.Turns)
	fmt.Fprintf(w, "%s%s  -  %s%s\n\n", p.dim, title, tl.Turns[0].Start.Local().Format("Mon 2 Jan 2006"), p.reset)

	stamp := func(at time.Time) string {
		if at.IsZero() {
			return ""
		}
		return at.Local().Format("15:04:05")
	}
	text := func(r Row) string {
		switch {
		case r.Message && r.Text == "":
			return p.dim + "(slash command)" + p.reset
		case r.Message:
			return `"` + snippet(r.Text) + `"`
		}
		return snippet(r.Text)
	}
	timed := func(r Row, colour, dur, bar, note string) {
		fmt.Fprintf(w, "%8s  %s%-7s %-7s %s%s  %s\n", stamp(r.At), colour, r.Kind, dur, bar, p.reset, note)
	}

	for _, r := range Rows(tl) {
		switch r.Kind {
		case RowStart:
			fmt.Fprintf(w, "%s  %-7s %s\n", stamp(r.At), r.Kind, text(r))
		case RowAgent:
			dur, note := Dur(r.D), ""
			if r.Approx {
				dur = "~" + dur
			}
			if r.Running {
				note = "still running"
			}
			timed(r, p.agent, dur, bar(r.D, scale, '█'), note)
		case RowWaiting:
			timed(r, p.you, Dur(r.D), bar(r.D, scale, '░'), ">> "+text(r))
		case RowClaimed, RowClosed:
			note := ""
			if r.Issue != nil {
				note = fmt.Sprintf("agent %s of %s", Dur(r.Issue.Active), Dur(r.Issue.Wall))
			}
			fmt.Fprintf(w, "%s  %s%-7s %s%s  %s\n", stamp(r.At), p.ait, r.Kind, r.Text, p.reset, note)
		}
	}

	active, blocked, between, wall := tl.Totals()
	summary := func(colour, label, value, note string) {
		fmt.Fprintf(w, "%s%-23s%s%7s  %s\n", colour, label, p.reset, value, note)
	}
	fmt.Fprintln(w)
	summary(p.agent, "Agent working", Dur(active), "")
	summary(p.you, "Waiting mid-turn", Dur(blocked), "(questions, permission prompts)")
	summary(p.you, "Waiting between turns", Dur(between), "")
	summary("", "Wall clock", Dur(wall), "")

	if len(tl.Issues) > 0 {
		fmt.Fprintf(w, "\n%sIssues%s\n", p.ait, p.reset)
		for _, is := range tl.Issues {
			end, span := "still open", ""
			if !is.Closed.IsZero() {
				end = "closed " + is.Closed.Local().Format("15:04")
				span = " of " + Dur(is.Wall) + " wall clock"
			}
			fmt.Fprintf(w, "  %-20s claimed %s, %s  -  agent %s%s\n", is.ID, is.Claimed.Local().Format("15:04"), end, Dur(is.Active), span)
		}
	}
}

// longest is the longest stretch of any kind in the session, which fills the
// full bar width.
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

// bar draws d on a square-root scale against the session's longest stretch:
// long waits still clearly dominate, short bursts of work stay visible, and
// nothing is capped. The exact figure sits next to it.
func bar(d, longest time.Duration, ch rune) string {
	frac := math.Sqrt(d.Seconds() / longest.Seconds())
	n := min(max(int(math.Ceil(frac*barWidth)), 1), barWidth)
	return strings.Repeat(string(ch), n) + strings.Repeat(" ", barWidth-n)
}

func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 50 {
		return string(r[:49]) + "…"
	}
	return s
}

// Dur formats a duration compactly: 13s, 5m37s, 1h02m.
func Dur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
