package views

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// Diff says what is different between two graphs.
//
// # Why this is worth having
//
// A drawing is checked into a repository and regenerated on every change, and
// the question a reviewer actually has is not "what does the estate look like"
// but "what did this change do to it". A picture answers that badly: two
// pictures side by side is a spot-the-difference puzzle, and the difference
// that matters is usually one box.
//
// It is also the payoff of a rule this project has had since v0.1: identical
// input produces byte-identical output. Without that, every regeneration would
// differ from the last one and a comparison would be noise.
//
// # An id is identity
//
// A resource whose id changed is a removal and an addition, not a rename.
// Nothing in either document says the two are the same thing — the ids are what
// say it, and they disagree — so matching them by similar names or similar
// attributes would be a claim neither file makes. That is the invention this
// project exists to avoid, and it is worse here than elsewhere: a wrongly
// paired rename hides a deletion and a creation behind a field change.
//
// Whoever knows it was a rename can say so, and nothing here has to guess.
//
// # What is compared, and what is not
//
// What is drawn, and what somebody would act on: nodes, edges, groups, routes
// and notes, with the fields that decide how each is drawn or what it claims.
//
// Measurements are not. Observations, metrics and log records change on every
// collection by design — that is what a measurement is — and a diff that
// reported them would bury the box that moved under a thousand numbers that
// were always going to move. What a *conclusion* drawn from measurements says
// is compared: a service that went blind is a change worth being told about,
// and coverage is a state rather than a reading.
//
// # Two estates are not a diff
//
// Comparing documents with different scopes reports every element of both as
// added and removed, which is not information. Diff refuses instead.
//
// A scope on one side and none on the other is that same mismatch and not a
// lesser one: ApplyScope rewrites every id to `scope:id`, so a scoped document
// and an unscoped one share no id at all.
func Diff(before, after *core.Graph) ([]Change, error) {
	if before == nil || after == nil {
		return nil, fmt.Errorf("a diff needs two graphs")
	}
	if a, b := scopeOf(before), scopeOf(after); a != b {
		return nil, fmt.Errorf(
			"these documents are about different estates (%s and %s): every element of both would be reported added and removed, which says nothing",
			namedScope(a), namedScope(b))
	}

	// Empty rather than absent, because a caller reading the JSON should not
	// have to tell "nothing changed" from "this field is missing".
	out := []Change{}
	out = append(out, compareSets(OfNode, nodeFields(before), nodeFields(after), labels(before, after), nil)...)
	out = append(out, compareSets(OfEdge, edgeFields(before), edgeFields(after), edgeLabels(before, after), nil)...)
	out = append(out, compareSets(OfGroup, groupFields(before), groupFields(after), groupLabels(before, after), nil)...)
	out = append(out, compareSets(OfPath, pathFields(before), pathFields(after), pathLabels(before, after),
		pathSubjects(before, after))...)
	out = append(out, compareSets(OfNote, noteFields(before), noteFields(after), noteLabels(before, after),
		noteSubjects(before, after))...)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].What != out[j].What {
			return out[i].What < out[j].What
		}
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// What a diff can say about one thing.
const (
	ChangeAdded   = "added"
	ChangeRemoved = "removed"
	ChangeChanged = "changed"
)

// What kind of thing it is about.
const (
	OfNode  = "node"
	OfEdge  = "edge"
	OfGroup = "group"
	OfPath  = "path"
	OfNote  = "note"
)

// ChangeKinds lists what a diff can say, and OfKinds what it can say it about.
func ChangeKinds() []string { return []string{ChangeAdded, ChangeRemoved, ChangeChanged} }

// OfKinds lists the things a diff compares.
func OfKinds() []string { return []string{OfNode, OfEdge, OfGroup, OfPath, OfNote} }

// ValidOf reports whether name is something a diff compares.
func ValidOf(name string) bool {
	for _, k := range OfKinds() {
		if k == name {
			return true
		}
	}
	return false
}

// Change is one thing that is different.
type Change struct {
	// Kind is added, removed or changed. What is the sort of thing it is
	// about. Subject is the id or key that thing is known by, which is what a
	// reader has to go on to find it in either document — a node or group id,
	// an edge key, a route key, or, for a note, the id of what the note is
	// about.
	//
	// It is deliberately not how the comparison told two things apart. A note
	// is identified by everything it is, text and all, and putting that in
	// Subject handed a caller a string matching nothing in either document —
	// with the note's own newlines in it, which is one record per line for
	// anything printing a table.
	Kind    string `json:"kind"`
	What    string `json:"what"`
	Subject string `json:"subject"`
	Label   string `json:"label,omitempty"`

	// Fields are what differ, on a change. A field named on both sides moved;
	// one named on neither is absent from both. Empty on an addition or a
	// removal, where the whole thing is the change.
	Fields []FieldChange `json:"fields,omitempty"`
}

// FieldChange is one field that moved.
type FieldChange struct {
	Field string `json:"field"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}

// compareSets is the whole comparison: two maps from identity to the fields
// that identity had, and the labels to call them by.
//
// Every kind of thing is compared the same way because every kind of thing is
// the same question — is it in both, and if so does it still say the same —
// and a second implementation of that per kind is a second place for the
// answers to drift apart.
func compareSets(what string, before, after map[string]map[string]string, labels, subjects map[string]string) []Change {
	var out []Change
	for _, id := range union(before, after) {
		was, there := before[id]
		now, here := after[id]
		subject := id
		if named, ok := subjects[id]; ok {
			subject = named
		}
		change := Change{What: what, Subject: subject, Label: labels[id]}
		switch {
		case !there:
			change.Kind = ChangeAdded
		case !here:
			change.Kind = ChangeRemoved
		default:
			change.Kind = ChangeChanged
			change.Fields = movedFields(was, now)
			if len(change.Fields) == 0 {
				continue
			}
		}
		out = append(out, change)
	}
	return out
}

func union(a, b map[string]map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]map[string]string{a, b} {
		for id := range m {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

func movedFields(was, now map[string]string) []FieldChange {
	var out []FieldChange
	seen := map[string]bool{}
	var names []string
	for _, m := range []map[string]string{was, now} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				names = append(names, k)
			}
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if was[name] != now[name] {
			out = append(out, FieldChange{Field: name, From: was[name], To: now[name]})
		}
	}
	return out
}

// The fields of each kind of thing: what it is, where it sits, what it claims.
// The identity — an id, an edge's four components, a route's key — is not
// among them, because a change to identity is a different thing rather than a
// changed one.

func nodeFields(g *core.Graph) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, n := range g.Nodes {
		f := map[string]string{"type": n.Type, "name": n.Name}
		put(f, "description", n.Description)
		put(f, "provider", n.Provider)
		for axis, path := range n.Groups {
			put(f, "group:"+axis, path)
		}
		if n.Coverage != nil {
			put(f, "coverage", string(n.Coverage.State))
		}
		put(f, "claim", claimText(n.Claim))
		attrFields(f, n.Attrs)
		out[n.ID] = f
	}
	return out
}

func edgeFields(g *core.Graph) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, e := range g.Edges {
		f := map[string]string{}
		if e.Suppressed {
			f["suppressed"] = "true"
		}
		put(f, "claim", claimText(e.Claim))
		attrFields(f, e.Attrs)
		out[core.EdgeKey(e.From, e.To, e.Kind, e.Relation)] = f
	}
	return out
}

func groupFields(g *core.Graph) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, gr := range g.Groups {
		f := map[string]string{"type": gr.Type, "label": gr.Label, "axis": gr.Axis}
		if gr.Parent != nil {
			put(f, "parent", *gr.Parent)
		}
		put(f, "claim", claimText(gr.Claim))
		attrFields(f, gr.Attrs)
		out[gr.ID] = f
	}
	return out
}

func pathFields(g *core.Graph) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, p := range g.Paths {
		f := map[string]string{"kind": string(p.Kind)}
		put(f, "label", p.Label)
		put(f, "claim", claimText(p.Claim))
		attrFields(f, p.Attrs)
		// A route's key names its participants in order, and the kind says
		// whether somebody declared it or something walked it. The same walk
		// declared and observed is two routes, and the document keeps them
		// apart, so a diff does too.
		out[string(p.Kind)+" "+p.Key()] = f
	}
	return out
}

// noteFields keys a note by everything it is, because a note is not a field
// somebody edits: `Normalize` folds only notes that match exactly, so a note
// with one word changed is a note somebody wrote, beside the one they wrote
// before.
func noteFields(g *core.Graph) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, n := range g.Notes {
		out[noteKey(n)] = map[string]string{}
	}
	return out
}

// noteKey tells two notes apart. It never leaves this package: what a change
// carries is the id of what the note is about.
func noteKey(n core.Note) string {
	return n.Subject + "\x00" + claimText(n.Claim) + "\x00" + n.Text
}

// The labels: what to call each subject on a line somebody reads. They come
// from both documents because a removed thing is only in one of them.

func labels(before, after *core.Graph) map[string]string {
	out := map[string]string{}
	for _, g := range []*core.Graph{before, after} {
		for _, n := range g.Nodes {
			out[n.ID] = orDefault(n.Name, n.ID)
		}
	}
	return out
}

func edgeLabels(before, after *core.Graph) map[string]string {
	out := map[string]string{}
	name := labels(before, after)
	group := groupLabels(before, after)
	end := func(id string) string {
		if n, ok := name[id]; ok {
			return n
		}
		if n, ok := group[id]; ok {
			return n
		}
		return id
	}
	for _, g := range []*core.Graph{before, after} {
		for _, e := range g.Edges {
			// The kind belongs on the label. Two lines between the same pair —
			// one the configuration declares and one something walked — are
			// two edges, and a label that showed only the ends would print
			// them identically.
			label := end(e.From) + " → " + end(e.To) + " (" + string(e.Kind)
			if e.Relation != "" {
				label += " " + e.Relation
			}
			out[core.EdgeKey(e.From, e.To, e.Kind, e.Relation)] = label + ")"
		}
	}
	return out
}

func groupLabels(before, after *core.Graph) map[string]string {
	out := map[string]string{}
	for _, g := range []*core.Graph{before, after} {
		for _, gr := range g.Groups {
			out[gr.ID] = orDefault(gr.Label, gr.ID)
		}
	}
	return out
}

func pathLabels(before, after *core.Graph) map[string]string {
	out := map[string]string{}
	for _, g := range []*core.Graph{before, after} {
		for _, p := range g.Paths {
			// The kind belongs on the label for the same reason it does on an
			// edge: the same walk declared and observed is two routes, and a
			// label showing only the participants would print them
			// identically.
			out[string(p.Kind)+" "+p.Key()] = PathLabel(g, p) + " (" + string(p.Kind) + ")"
		}
	}
	return out
}

// pathSubjects and noteSubjects are what to call each thing in a document,
// where that is not the same as how the comparison told it apart.
//
// A route is identified by its kind as well as its participants, because the
// same walk declared and observed is two routes; its key names the
// participants alone, and that is the reversible thing a caller can look up.
//
// A note is identified by everything it is, and what it is *about* is a node
// or group id — the same kind of subject every other change carries.
func pathSubjects(before, after *core.Graph) map[string]string {
	out := map[string]string{}
	for _, g := range []*core.Graph{before, after} {
		for _, p := range g.Paths {
			out[string(p.Kind)+" "+p.Key()] = p.Key()
		}
	}
	return out
}

func noteSubjects(before, after *core.Graph) map[string]string {
	out := map[string]string{}
	for _, g := range []*core.Graph{before, after} {
		for _, n := range g.Notes {
			out[noteKey(n)] = n.Subject
		}
	}
	return out
}

func noteLabels(before, after *core.Graph) map[string]string {
	out := map[string]string{}
	name := labels(before, after)
	for _, g := range []*core.Graph{before, after} {
		for _, n := range g.Notes {
			about := n.Subject
			if named, ok := name[n.Subject]; ok {
				about = named
			}
			out[noteKey(n)] = about + ": " + firstLine(n.Text)
		}
	}
	return out
}

func firstLine(text string) string {
	if at := strings.IndexAny(text, "\r\n"); at >= 0 {
		return strings.TrimSpace(text[:at]) + " …"
	}
	return strings.TrimSpace(text)
}

func put(f map[string]string, name, value string) {
	if value != "" {
		f[name] = value
	}
}

// attrFields flattens a resource's own attributes.
//
// They are compared because they are where the change usually is: an instance
// type, a port, an image tag. Each is encoded the way the document encodes it,
// so that a number and the string of that number are told apart.
func attrFields(f map[string]string, attrs map[string]any) {
	for k, v := range attrs {
		raw, err := json.Marshal(v)
		if err != nil {
			// Nothing that reached a validated document can fail to encode;
			// if one ever does, say the field moved rather than pretend it
			// did not exist.
			f["attr:"+k] = fmt.Sprintf("%v", v)
			continue
		}
		f["attr:"+k] = string(raw)
	}
}

func claimText(c *core.Claim) string {
	if c == nil {
		return ""
	}
	if c.Author == "" {
		return string(c.Origin)
	}
	return string(c.Origin) + "/" + c.Author
}

// namedScope is what to call a scope in a sentence. A document that names none
// has to be called something, or the sentence reads as though a name were
// missing from it rather than from the document.
func namedScope(scope string) string {
	if scope == "" {
		return "no scope"
	}
	return scope
}

func scopeOf(g *core.Graph) string {
	if g.Metadata == nil {
		return ""
	}
	return g.Metadata.Scope
}
