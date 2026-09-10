package views

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
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
	Than   *Than    `json:"than,omitempty"`
	Since  string   `json:"since,omitempty"`
}

// Than is a bound taken from another reading rather than written down.
//
// # Why a baseline is a reading
//
// "Twice what it usually is" needs a usual, and nothing here computes one. A
// usual is a moving average, or a median of the same hour last week, or a
// quantile over a season — every one of them a choice about the estate, made
// over history this program does not keep and does not want to.
//
// So the baseline arrives the way every other outside fact arrives: a
// collector holds the credentials and the vendor's query language, works out
// whatever it likes, and writes the answer back as an ordinary observation.
// The rule then says which reading is the baseline, and a bound becomes a
// comparison between two numbers that were both measured.
//
// # Why there is a multiplier and nothing else
//
// Times is policy, and policy belongs in the document somebody reviews:
// "twice what it usually is" is a decision an operator argues about and
// changes, and baking it into a collector means redeploying to move a
// threshold.
//
// Anything past one factor is arithmetic. A rule cannot say "the average plus
// three standard deviations", because a sum of terms is an expression and an
// expression needs an evaluator — which this package refuses on the same
// grounds it refuses one everywhere else. Three sigma is a number a collector
// can work out and write down as one reading; that is the boundary, and it is
// the same boundary as everywhere.
type Than struct {
	// Metric is the other reading, on the same subject.
	Metric string `json:"metric"`

	// Times multiplies it. Absent means once.
	Times *float64 `json:"times,omitempty"`
}

// factor is what to multiply the baseline by.
func (t *Than) factor() float64 {
	if t == nil || t.Times == nil {
		return 1
	}
	return *t.Times
}

// Alert is one rule finding one subject.
type Alert struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity,omitempty"`
	Subject  string `json:"subject"`
	Label    string `json:"label,omitempty"`
	Reason   string `json:"reason"`

	// Is is the condition that fired, and it is what a reader has to go on to
	// know what the rest of the alert means. The reason is a sentence written
	// for the condition — a bound names its value in it, a silence names its
	// moment — so anything deciding what to do with Value and LastSeen has to
	// ask which condition wrote it. Looking for the digits in the sentence
	// instead answers a different question, and answers it wrong the first
	// time a value is 0 or 1.
	Is string `json:"is"`

	// Metric is what was measured, when the rule was about a measurement. A
	// value with no name beside it is a number somebody has to go and look up,
	// and the rule knew it all along.
	Metric string   `json:"metric,omitempty"`
	Value  *float64 `json:"value,omitempty"`

	// Bound is the number Value was compared against, and Baseline is the
	// reading the bound was derived from when it was not written down.
	//
	// They are fields for the reason stated a few lines above: the reason is a
	// sentence, and digging a number back out of a sentence is a different
	// question with a nearly-right answer. A bound that lived only in prose
	// left anything reading the JSON with no way to say how far over it was.
	Bound          *float64 `json:"bound,omitempty"`
	Baseline       *float64 `json:"baseline,omitempty"`
	BaselineMetric string   `json:"baseline_metric,omitempty"`

	LastSeen string `json:"last_seen,omitempty"`
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
		if r.When.Value == nil && r.When.Than == nil {
			return fmt.Errorf("%q needs something to compare against: a value, or another reading in than", r.When.Is)
		}
		if r.When.Value != nil && r.When.Than != nil {
			return fmt.Errorf("%q has both a value and a than: a bound is one number or the other reading, and a rule with two of them does not say which it meant", r.When.Is)
		}
		if r.When.Than != nil {
			if r.When.Than.Metric == "" {
				return fmt.Errorf("than needs the metric that carries the baseline")
			}
			if r.When.Than.Metric == r.When.Metric {
				return fmt.Errorf("than names %q, which is what is being compared: a reading is not a baseline for itself, and the comparison can never be true", r.When.Metric)
			}
			if t := r.When.Than.Times; t != nil && (*t <= 0 || math.IsInf(*t, 0) || math.IsNaN(*t)) {
				return fmt.Errorf("than times %v is not a factor: write a value for a fixed bound", *t)
			}
		}
	case Quiet:
		if r.When.Metric == "" {
			return fmt.Errorf("quiet needs a metric that has gone quiet")
		}
		if r.When.Value != nil || r.When.Than != nil {
			return fmt.Errorf("quiet is about whether anything arrived, not about what it said: a bound on it is a thing the rule does not do, and one written down would be read by nobody")
		}
		if r.When.Since == "" {
			return fmt.Errorf("quiet needs a moment to be quiet since: write one in the rule, or give the run a --since; how long is too long is not this program's judgement")
		}
	case Unexpected, Unused, Partial:
		if r.When.Metric != "" || r.When.Value != nil || r.When.Than != nil {
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
// The second return is what the rules could not answer. A rule that was
// applied and found nothing is not the same as a rule that had nothing to
// apply itself to, and a caller that cannot tell them apart reports silence
// either way. Nothing here writes to a stream: who says it, and where, is the
// caller's business, like everything else in this package.
func Alerts(g *core.Graph, doc *Rules) ([]Alert, []string, error) {
	if g == nil || doc == nil {
		return nil, nil, fmt.Errorf("nothing to check")
	}
	// Empty rather than absent, because a caller reading the JSON should not
	// have to tell "nothing fired" from "this field is missing".
	out := []Alert{}
	var unanswered []string
	for _, rule := range doc.Rules {
		found, notices, err := rule.against(g)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", rule.Name, err)
		}
		sort.SliceStable(found, func(i, j int) bool { return found[i].Subject < found[j].Subject })
		out = append(out, found...)
		unanswered = append(unanswered, notices...)
	}
	return out, unanswered, nil
}

func (r Rule) against(g *core.Graph) ([]Alert, []string, error) {
	switch r.When.Is {
	case Above, Below, Quiet:
		found, notices := r.readings(g)
		return found, notices, nil
	default:
		found, err := r.routes(g)
		return found, nil, err
	}
}

// newestOf is the newest reading of one metric per subject, which is what a
// bound is about: a service that was over its limit last week and is not now is
// not something to wake somebody for.
//
// The baseline is read the same way and through the same window as the reading
// it bounds. A baseline from before the window is a usual from another era, and
// comparing today against it is a comparison nobody asked for.
func (r Rule) newestOf(g *core.Graph, metric string) map[string]core.Observation {
	newest := map[string]core.Observation{}
	for _, o := range g.Observations {
		if o.Metric != metric || !r.covers(g, o.Subject) {
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
	return newest
}

// readings applies a rule that is about a measurement.
func (r Rule) readings(g *core.Graph) ([]Alert, []string) {
	newest := r.newestOf(g, r.When.Metric)

	var out []Alert
	if r.When.Is == Quiet {
		// A subject with no reading at all is quiet too, and it is the case a
		// bound cannot see: nothing arrived, so nothing was compared.
		var undated []string
		for _, subject := range r.subjects(g) {
			at, seen := newest[subject]
			// A reading that does not say when it was taken cannot settle
			// this. Quiet is the claim that nothing arrived, and something
			// did — so it does not fire. The reading cannot be placed inside
			// the window either — so it does not pass. The rule is not
			// answering the question for this subject, and saying so is the
			// only honest thing left.
			//
			// The alternative was to keep treating an undated reading as
			// older than every moment, which is what before does and what is
			// right everywhere else. Here it made every subject of a collector
			// that records no time fire, every run, with a reason naming a
			// moment that was nowhere in the document.
			if seen && at.ObservedAt == "" {
				undated = append(undated, subject)
				continue
			}
			if seen && !before(at.ObservedAt, r.When.Since) {
				continue
			}
			alert := Alert{
				Rule: r.Name, Severity: r.Severity, Subject: subject, Label: labelOf(g, subject),
				Is: r.When.Is, Metric: r.When.Metric,
				Reason: fmt.Sprintf("nothing measured %s since %s", r.When.Metric, r.When.Since),
			}
			if seen {
				alert.LastSeen, alert.Value = at.ObservedAt, at.Value
				alert.Reason = fmt.Sprintf("%s last measured %s", r.When.Metric, at.ObservedAt)
			}
			out = append(out, alert)
		}
		if len(undated) > 0 {
			sort.Strings(undated)
			return out, []string{fmt.Sprintf(
				"%s: %s measured with no time on the reading, so there is no telling whether %s went quiet; not reported",
				r.Name, subjectList(undated), theyOrIt(len(undated)))}
		}
		return out, nil
	}

	// The baseline, when the bound is another reading rather than a number.
	var baseline map[string]core.Observation
	if r.When.Than != nil {
		baseline = r.newestOf(g, r.When.Than.Metric)
	}

	word := "above"
	if r.When.Is == Below {
		word = "below"
	}
	// Why each subject could not be judged, kept apart by cause. They have
	// different fixes — a collector nobody ran, a collector writing readings
	// with no number in them, a usual a multiplier cannot mean anything
	// against — and one message covering all three sends its readers looking
	// for the wrong thing.
	unjudged := map[string][]string{}
	for subject, o := range newest {
		if o.Value == nil {
			unjudged[noValue] = append(unjudged[noValue], subject)
			continue
		}
		limit, against, why := r.boundFor(subject, baseline)
		if why != "" {
			// The rule is not answering the question for this subject.
			// Firing would be a comparison against nothing; passing quietly
			// would say the reading was fine.
			unjudged[why] = append(unjudged[why], subject)
			continue
		}
		over := *o.Value > limit
		if r.When.Is == Below {
			over = *o.Value < limit
		}
		if !over {
			continue
		}
		alert := Alert{
			Rule: r.Name, Severity: r.Severity, Subject: subject, Label: labelOf(g, subject),
			Is: r.When.Is, Metric: r.When.Metric, Value: o.Value, LastSeen: o.ObservedAt,
			Bound:  &limit,
			Reason: fmt.Sprintf("%s is %g, %s %s", r.When.Metric, *o.Value, word, against),
		}
		if r.When.Than != nil {
			at := baseline[subject]
			alert.Baseline, alert.BaselineMetric = at.Value, r.When.Than.Metric
		}
		out = append(out, alert)
	}
	return out, r.notices(unjudged)
}

// Why a subject with a reading could not be judged after all.
const (
	noValue          = "no-value"
	noBaseline       = "no-baseline"
	noBaselineValue  = "no-baseline-value"
	unusableBaseline = "unusable-baseline"
)

// notices turns what could not be judged into sentences, one per cause.
func (r Rule) notices(unjudged map[string][]string) []string {
	if len(unjudged) == 0 {
		return nil
	}
	sentences := map[string]string{
		noValue:          "measured %s with no number on the reading, so there is nothing to compare; not reported",
		noBaseline:       "nothing measured %s, so there is nothing to judge " + r.When.Metric + " by; not reported",
		noBaselineValue:  "measured %s with no number on it, so there is no baseline to judge " + r.When.Metric + " by; not reported",
		unusableBaseline: "measured %s at zero or below, and a multiplier cannot mean anything against it — twice a usual of minus one hundred is minus two hundred, which every healthy reading is above; not reported",
	}
	named := map[string]string{
		noValue: r.When.Metric,
	}
	if r.When.Than != nil {
		for _, cause := range []string{noBaseline, noBaselineValue, unusableBaseline} {
			named[cause] = r.When.Than.Metric
		}
	}

	var out []string
	for _, cause := range []string{noValue, noBaseline, noBaselineValue, unusableBaseline} {
		subjects := unjudged[cause]
		if len(subjects) == 0 {
			continue
		}
		sort.Strings(subjects)
		out = append(out, fmt.Sprintf("%s: %s: "+sentences[cause],
			r.Name, subjectList(subjects), named[cause]))
	}
	return out
}

// boundFor is the number this subject's reading is compared against, and how to
// say what it was. The third return is why there is none, or "" when there is.
//
// A rule that names a value has the same bound for every subject. A rule that
// names another reading has a bound per subject, and a subject with no such
// reading has none at all — which is not a bound of zero, and not a pass.
func (r Rule) boundFor(subject string, baseline map[string]core.Observation) (limit float64, against, why string) {
	if r.When.Than == nil {
		return *r.When.Value, fmt.Sprintf("%g", *r.When.Value), ""
	}
	at, seen := baseline[subject]
	if !seen {
		return 0, "", noBaseline
	}
	if at.Value == nil {
		return 0, "", noBaselineValue
	}
	times := r.When.Than.factor()
	// A multiplier is a claim about magnitude, and magnitude is measured from
	// zero. Twice a usual of minus one hundred is minus two hundred, which
	// every healthy reading of a metric that can go negative — a margin, a
	// remaining budget, a difference — is above; the rule would then fire on
	// the usual itself and on everything better than it, forever.
	//
	// A factor of one multiplies nothing, so it is left alone: there the
	// collector wrote the threshold and the rule only names it, and a
	// threshold of minus five is an ordinary thing to want.
	if times != 1 && *at.Value <= 0 {
		return 0, "", unusableBaseline
	}
	limit = *at.Value * times
	// The sentence says what the bound was made of, because "above 3000" and
	// "above twice the usual, which was 1500" are different things to be told
	// at three in the morning.
	if times == 1 {
		return limit, fmt.Sprintf("%s (%g)", r.When.Than.Metric, *at.Value), ""
	}
	return limit, fmt.Sprintf("%g× %s (%g)", times, r.When.Than.Metric, *at.Value), ""
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
			Is: f.Kind, Reason: f.Reason,
			// The walks the finding counted. It is a measurement like any
			// other and it has a name, which is the name the collector wrote
			// it under — without one, a listing prints a number and leaves the
			// reader to guess what was counted.
			Metric: DefaultPathMetric, Value: f.Requests, LastSeen: f.LastSeen,
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
		// The route the document holds, not one rebuilt from its key. A
		// rebuilt one has the participants and none of what the document says
		// about them — the way in, above all — so the same route was named two
		// ways in one run: with its API on the line a rule fired on, and
		// without it on the line beside it.
		//
		// A subject is a walk, and a walk can be recorded more than once: what
		// the routing table declares and what the traces observed are two
		// paths with one key, and only one of them was told where requests
		// come in. So the one with something to say is the one asked, rather
		// than whichever kind happens to sort first. A path with nothing to
		// say names the walk, which is what the rebuilt one below does.
		for _, p := range g.Paths {
			if p.Key() == subject && (p.Label != "" || len(EntryOf(p)) > 0) {
				return PathLabel(g, p)
			}
		}
		return PathLabel(g, core.Path{Nodes: nodes})
	}
	if n, ok := g.Node(subject); ok && n.Name != "" {
		return n.Name
	}
	return subject
}

// subjectList names what a notice is about, up to the point where naming them
// stops being information. A notice that prints two hundred ids is a notice
// people learn to scroll past.
func subjectList(subjects []string) string {
	const most = 5
	if len(subjects) <= most {
		return strings.Join(subjects, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(subjects[:most], ", "), len(subjects)-most)
}

func theyOrIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "they"
}
