package core

import (
	"fmt"
	"sort"
	"strings"
)

// Note is what somebody wrote about one thing in the drawing.
//
// # Why this is not a field on the node
//
// A note is a claim, and claims here are things with an author. A string on
// the node would be one more attribute of the resource — the graph saying so
// itself — when the whole point is that a person said it, and which person.
// Kept beside the graph with a subject and a claim, it sits exactly where an
// observation sits, and for the same reason: evidence about a subject rather
// than a property of it.
//
// Several notes about one thing are several notes. Two people writing about
// the same service is the ordinary case, and the later one does not replace
// the earlier one any more than a second opinion deletes the first.
//
// # Why the text is Markdown
//
// Because it is what people already write, and because the alternative is
// deciding what a note may contain. The renderers are the ones that must be
// careful with it: it is somebody else's text arriving in a document that gets
// drawn, and the viewer builds elements rather than markup for exactly that
// reason.
type Note struct {
	// Subject is a node id, a group id, or an edge key.
	Subject string `json:"subject"`

	// Text is Markdown, as written.
	Text string `json:"text"`

	// Claim is who wrote it. A note by nobody is an unsigned sentence in a
	// document whose subject is who said what, so this one is not optional in
	// practice — an overlay always supplies it.
	Claim *Claim `json:"claim,omitempty"`
}

// NotesAbout returns the notes written about one subject, in stored order.
func (g *Graph) NotesAbout(subject string) []Note {
	var out []Note
	for _, n := range g.Notes {
		if n.Subject == subject {
			out = append(out, n)
		}
	}
	return out
}

// normalizeNotes sorts and folds notes that say the same thing about the same
// subject with the same claim.
//
// The same note twice is one note — an overlay applied twice, or two documents
// carrying the same sentence — but two notes that differ in a single character
// are two notes, because this package has no way to know which of them the
// writer meant to keep.
func (g *Graph) normalizeNotes() {
	if len(g.Notes) == 0 {
		return
	}
	sort.SliceStable(g.Notes, func(i, j int) bool {
		a, b := g.Notes[i], g.Notes[j]
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Text != b.Text {
			return a.Text < b.Text
		}
		return claimLess(claimOrParser(a.Claim), claimOrParser(b.Claim))
	})

	folded := g.Notes[:0]
	for _, n := range g.Notes {
		if len(folded) > 0 {
			at := folded[len(folded)-1]
			same := at.Subject == n.Subject && at.Text == n.Text &&
				claimOrParser(at.Claim) == claimOrParser(n.Claim)
			if same {
				continue
			}
		}
		folded = append(folded, n)
	}
	g.Notes = folded
}

// checkNotes reports what is wrong with the notes in this document.
func (g *Graph) checkNotes(ids map[string]bool) []string {
	var problems []string
	for i, n := range g.Notes {
		where := fmt.Sprintf("note %d", i)
		if strings.TrimSpace(n.Text) == "" {
			problems = append(problems, where+": empty text")
		}
		switch {
		case n.Subject == "":
			problems = append(problems, where+": empty subject")
		case ids[n.Subject]:
		case g.HasConflictTarget(n.Subject, ConflictTargetEdge):
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown subject %q", where, n.Subject))
		}
		problems = append(problems, checkClaim(n.Claim, where)...)
	}
	return problems
}
