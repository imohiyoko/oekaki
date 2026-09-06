package views

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/schema"
)

// Rules are the answer to "tell me when this is wrong" — as a document rather
// than as an expression language.
//
// A document can be read by somebody who did not write it, reviewed in a pull
// request, and diffed between two versions of an estate. An expression needs an
// evaluator, an evaluator needs a sandbox, and a sandbox is a thing to maintain
// forever in a program whose whole point is somewhere else.
//
// What that costs is real and worth stating: a rule here cannot say anything
// this file does not already know how to compute. The escape hatch is the
// boundary this project already has — a collector holds the credentials and the
// vendor's own query language, works out whatever it likes, and writes the
// conclusion back as an ordinary observation, which a rule can then be about.
//
// # Nothing here reads a clock
//
// A rule may ask about a moment, and the moment arrives already decided. "Thirty
// days" is a question about today, and today belongs to whoever is running the
// rules — so the same document and the same graph produce the same alerts, and
// an alert is something that can be committed and argued with.

// What a rule can be.
const (
	// Above and Below are a bound on the newest reading. Above is the spike;
	// below is the silence a collector reports as a zero.
	Above = "above"
	Below = "below"
)

// The other conditions a rule may ask for are the findings a path listing
// already makes — Quiet, Unexpected, Unused and Partial, declared with the
// listing itself. A rule naming one is asking for that comparison, not for a
// second implementation of it, and reusing the names is what keeps the two
// from drifting into meaning different things.

// Rules is a document somebody wrote.
type Rules struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Note    string `json:"note,omitempty"`
	Rules   []Rule `json:"rules"`
}

// Rule is one thing worth being told about.
type Rule struct {
	Name     string `json:"name"`
	Severity string `json:"severity,omitempty"`
	About    About  `json:"about,omitempty"`
	When     When   `json:"when"`
}

// About narrows what a rule is about. Empty means everything the condition can
// apply to.
type About struct {
	Subject string `json:"subject,omitempty"`
	Type    string `json:"type,omitempty"`
	Through string `json:"through,omitempty"`

	// Kind is "node" or "path", and empty means both.
	//
	// It earns its place on the condition that enumerates subjects. A rule
	// asking what has stopped reporting a route's metric, with nothing said
	// about what it is about, is also asking about every box in the estate —
	// and every box has indeed never reported a metric that was never about
	// boxes. The answer is true and useless, which is the worst kind of alert.
	Kind string `json:"kind,omitempty"`
}

// When is what makes a rule fire.
type When struct {
	Is     string   `json:"is"`
	Metric string   `json:"metric,omitempty"`
	Value  *float64 `json:"value,omitempty"`
	Since  string   `json:"since,omitempty"`
}

// Alert is one rule finding one subject.
type Alert struct {
	Rule     string   `json:"rule"`
	Severity string   `json:"severity,omitempty"`
	Subject  string   `json:"subject"`
	Label    string   `json:"label,omitempty"`
	Reason   string   `json:"reason"`
	Value    *float64 `json:"value,omitempty"`
	LastSeen string   `json:"last_seen,omitempty"`
}

// ParseRules reads a rules document and checks it against the published schema
// before anything is done with it.
//
// The moment arrives with the document, because half of what makes a rule
// sound depends on it: a rule that asks what has gone quiet needs one, and a
// document cannot supply it — "thirty days" is a question about today, and a
// document has no today. So whoever runs the rules resolves a moment and hands
// it in, and a rule that named none is completed with it before it is judged.
//
// Empty means no moment was given, which is only an error for the rules that
// cannot do without one.
func ParseRules(raw []byte, since string) (*Rules, error) {
	if err := schema.ValidateRules(raw); err != nil {
		return nil, err
	}
	var doc Rules
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing rules: %w", err)
	}
	for i := range doc.Rules {
		if doc.Rules[i].When.Since == "" {
			doc.Rules[i].When.Since = since
		}
		if err := doc.Rules[i].check(); err != nil {
			return nil, fmt.Errorf("rules[%d] (%s): %w", i, doc.Rules[i].Name, err)
		}
	}
	return &doc, nil
}

// check catches what a schema cannot: a condition asking for something it was
// not given. The schema says which fields exist; which of them each condition
// needs is knowledge this package has.
func (r Rule) check() error {
	switch r.When.Is {
	case Above, Below:
		if r.When.Metric == "" {
			return fmt.Errorf("%q needs a metric to compare", r.When.Is)
		}
		if r.When.Value == nil {
			return fmt.Errorf("%q needs a value to compare against", r.When.Is)
		}
	case Quiet:
		if r.When.Metric == "" {
			return fmt.Errorf("quiet needs a metric that has gone quiet")
		}
		if r.When.Since == "" {
			return fmt.Errorf("quiet needs a moment to be quiet since: write one in the rule, or give the run a --since; how long is too long is not this program's judgement")
		}
	case Unexpected, Unused, Partial:
		if r.When.Metric != "" || r.When.Value != nil {
			return fmt.Errorf("%q is a comparison of routes and reads no metric", r.When.Is)
		}
	default:
		return fmt.Errorf("unknown condition %q", r.When.Is)
	}

	// A document has no clock, so a moment written into one is a moment, not a
	// span. "30d" is what somebody types on the command line, and left
	// unchecked it reaches a comparison that falls back to comparing the text
	// — where every timestamp sorts before the letter "d", so every subject
	// fires. A false alert is bad; a false alert that stops a pipeline under
	// --exit-code is worse.
	if r.When.Since != "" {
		if _, err := time.Parse(time.RFC3339, r.When.Since); err != nil {
			return fmt.Errorf("since %q is not a moment: a rule takes an RFC3339 time, and a span like 30d belongs on the command line, which has a clock to resolve it against", r.When.Since)
		}
	}
	return nil
}

// Alerts runs a document against a graph.
//
// The order is the document's own, then the subject: a person reading a list
// wants their most important rule at the top, and they said which that was by
// writing it first.
func Alerts(g *core.Graph, doc *Rules) ([]Alert, error) {
	if g == nil || doc == nil {
		return nil, fmt.Errorf("nothing to check")
	}
	// Empty rather than absent, because a caller reading the JSON should not
	// have to tell "nothing fired" from "this field is missing".
	out := []Alert{}
	for _, rule := range doc.Rules {
		found, err := rule.against(g)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rule.Name, err)
		}
		sort.SliceStable(found, func(i, j int) bool { return found[i].Subject < found[j].Subject })
		out = append(out, found...)
	}
	return out, nil
}

func (r Rule) against(g *core.Graph) ([]Alert, error) {
	switch r.When.Is {
	case Above, Below, Quiet:
		return r.readings(g), nil
	default:
		return r.routes(g)
	}
}

// readings applies a rule that is about a measurement.
func (r Rule) readings(g *core.Graph) []Alert {
	// The newest reading per subject, which is what a bound is about: a
	// service that was over its limit last week and is not now is not
	// something to wake somebody for.
	newest := map[string]core.Observation{}
	for _, o := range g.Observations {
		if o.Metric != r.When.Metric || !r.covers(g, o.Subject) {
			continue
		}
		// A window is about readings that say when they were taken. One that
		// does not is not old — it is undated, and dropping it silently would
		// take every reading from a collector that records no time out of
		// every bound, which is a rule that stops working for a reason nobody
		// can see.
		if r.When.Since != "" && r.When.Is != Quiet && o.ObservedAt != "" && before(o.ObservedAt, r.When.Since) {
			continue
		}
		at, seen := newest[o.Subject]
		if !seen || !before(o.ObservedAt, at.ObservedAt) {
			newest[o.Subject] = o
		}
	}

	var out []Alert
	if r.When.Is == Quiet {
		// A subject with no reading at all is quiet too, and it is the case a
		// bound cannot see: nothing arrived, so nothing was compared.
		for _, subject := range r.subjects(g) {
			at, seen := newest[subject]
			if seen && !before(at.ObservedAt, r.When.Since) {
				continue
			}
			alert := Alert{
				Rule: r.Name, Severity: r.Severity, Subject: subject, Label: labelOf(g, subject),
				Reason: fmt.Sprintf("nothing measured %s since %s", r.When.Metric, r.When.Since),
			}
			if seen {
				alert.LastSeen, alert.Value = at.ObservedAt, at.Value
				alert.Reason = fmt.Sprintf("%s last measured %s", r.When.Metric, at.ObservedAt)
			}
			out = append(out, alert)
		}
		return out
	}

	for subject, o := range newest {
		if o.Value == nil {
			continue
		}
		over := *o.Value > *r.When.Value
		if r.When.Is == Below {
			over = *o.Value < *r.When.Value
		}
		if !over {
			continue
		}
		word := "above"
		if r.When.Is == Below {
			word = "below"
		}
		out = append(out, Alert{
			Rule: r.Name, Severity: r.Severity, Subject: subject, Label: labelOf(g, subject),
			Value: o.Value, LastSeen: o.ObservedAt,
			Reason: fmt.Sprintf("%s is %g, %s %g", r.When.Metric, *o.Value, word, *r.When.Value),
		})
	}
	return out
}

// routes applies a rule that is about what the declared and the observed
// routes say about each other. The comparison itself is Paths': one reading of
// it, wherever it is asked for.
func (r Rule) routes(g *core.Graph) ([]Alert, error) {
	findings, err := Paths(g, PathOptions{Since: r.When.Since})
	if err != nil {
		return nil, err
	}
	var out []Alert
	for _, f := range findings {
		if f.Kind != r.When.Is || !r.covers(g, f.Key) {
			continue
		}
		out = append(out, Alert{
			Rule: r.Name, Severity: r.Severity, Subject: f.Key, Label: PathLabel(g, f.Path),
			Reason: f.Reason, Value: f.Requests, LastSeen: f.LastSeen,
		})
	}
	return out, nil
}

// subjects is everything a rule could be about, for the conditions that have
// to notice an absence. A rule about nothing in particular is about every node
// and every route, because either can go quiet.
func (r Rule) subjects(g *core.Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		if r.covers(g, n.ID) {
			out = append(out, n.ID)
		}
	}
	for _, p := range g.Paths {
		if key := p.Key(); r.covers(g, key) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

// covers reports whether a subject is one this rule is about.
func (r Rule) covers(g *core.Graph, subject string) bool {
	if r.About.Subject != "" && r.About.Subject != subject {
		return false
	}
	if _, isPath := core.ParsePathKey(subject); r.About.Kind != "" {
		if (r.About.Kind == "path") != isPath {
			return false
		}
	}
	if r.About.Type != "" {
		n, ok := g.Node(subject)
		if !ok || n.Type != r.About.Type {
			return false
		}
	}
	if r.About.Through != "" {
		nodes, isPath := core.ParsePathKey(subject)
		if !isPath {
			return subject == r.About.Through
		}
		if !contains(nodes, r.About.Through) {
			return false
		}
	}
	return true
}

// labelOf is what to call a subject in a list somebody reads.
func labelOf(g *core.Graph, subject string) string {
	if nodes, isPath := core.ParsePathKey(subject); isPath {
		return PathLabel(g, core.Path{Nodes: nodes})
	}
	if n, ok := g.Node(subject); ok && n.Name != "" {
		return n.Name
	}
	return subject
}
