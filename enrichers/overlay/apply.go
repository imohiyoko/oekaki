package overlay

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
	"github.com/imohiyoko/oekaki/providers"
)

// Policy decides what happens to an assertion that names nothing in the graph.
type Policy string

const (
	// PolicyAdopt turns the selector into a node of its own.
	PolicyAdopt Policy = "adopt"
	// PolicyReport drops the assertion but lists it.
	PolicyReport Policy = "report"
	// PolicyError refuses to finish.
	PolicyError Policy = "error"
)

// Valid reports whether p is a policy that exists.
func (p Policy) Valid() bool {
	switch p {
	case PolicyAdopt, PolicyReport, PolicyError:
		return true
	}
	return false
}

// Options tunes the enricher.
type Options struct {
	// Unmatched decides what happens to an assertion naming nothing.
	//
	// Adopt is the default, and it is the most opinionated call in this
	// package. The alternative — report and move on — loses the finding in a
	// stream of stderr that scrolls away, while the diagram, which is what
	// people actually look at, comes out clean. And an observed log stream
	// that maps to nothing in the infrastructure is not noise: it is the most
	// valuable thing a coverage map can produce, because it means there is a
	// system here that nobody has modelled.
	Unmatched Policy
}

type enricher struct {
	docs []*Document
	opts Options
}

// New returns an enricher that applies the documents in the order given.
func New(docs []*Document, opts Options) enrichers.Enricher {
	if !opts.Unmatched.Valid() {
		opts.Unmatched = PolicyAdopt
	}
	return &enricher{docs: docs, opts: opts}
}

func (e *enricher) Name() string { return "overlay" }

// tally accumulates what has been claimed about one node's logging, so that
// the state can be decided once at the end rather than being rewritten by
// every assertion that arrives.
type tally struct {
	declared bool

	// seen means logs were reported arriving. An observation that counted
	// zero is not "seen": somebody looked at the destination and found it
	// empty, which is the finding, not the opposite of one.
	seen bool

	// looked means somebody checked and reported nothing — either a
	// logs.none, or an observation whose count was zero.
	looked bool

	evidence []core.Evidence
}

// nodeFieldClaims keeps provenance for independently asserted node fields.
// Node has one summary Claim in the public IR, but using that summary while
// resolving the next field lets an unrelated name assertion decide a later
// type assertion. The per-field claims remain internal and are folded back to
// one deterministic summary after every document has been applied.
type trackedNodeFieldAssertion struct {
	value    string
	claim    core.Claim
	explicit bool
}

type nodeFieldHistory struct {
	assertions []trackedNodeFieldAssertion
}

type nodeFieldClaims map[string]map[string]*nodeFieldHistory

func newNodeFieldClaims(g *core.Graph) nodeFieldClaims {
	claims := nodeFieldClaims{}
	for i := range g.Nodes {
		node := &g.Nodes[i]
		claims[node.ID] = map[string]*nodeFieldHistory{
			"type": newNodeFieldHistory(node.Type, node.Claim),
			"name": newNodeFieldHistory(node.Name, node.Claim),
		}
	}
	return claims
}

func newNodeFieldHistory(value string, claim *core.Claim) *nodeFieldHistory {
	history := &nodeFieldHistory{}
	history.assertions = append(history.assertions, trackedNodeFieldAssertion{
		value: value, claim: claimOrParser(claim), explicit: claim != nil,
	})
	return history
}

func (claims nodeFieldClaims) forNode(g *core.Graph, id string) map[string]*nodeFieldHistory {
	if fields, ok := claims[id]; ok {
		return fields
	}
	node, _ := g.Node(id)
	fields := map[string]*nodeFieldHistory{
		"type": newNodeFieldHistory("", nil),
		"name": newNodeFieldHistory("", nil),
	}
	if node != nil {
		fields["type"] = newNodeFieldHistory(node.Type, node.Claim)
		fields["name"] = newNodeFieldHistory(node.Name, node.Claim)
	}
	claims[id] = fields
	return fields
}

func (history *nodeFieldHistory) add(value string, claim core.Claim) {
	candidate := trackedNodeFieldAssertion{
		value: value, claim: claim, explicit: true,
	}
	for _, existing := range history.assertions {
		if existing.value == candidate.value && existing.explicit == candidate.explicit && claimsEqual(existing.claim, candidate.claim) {
			return
		}
	}
	history.assertions = append(history.assertions, candidate)
}

func (history *nodeFieldHistory) winner() trackedNodeFieldAssertion {
	winner := history.assertions[0]
	for _, candidate := range history.assertions[1:] {
		if trackedNodeFieldAssertionPreferred(candidate, winner) {
			winner = candidate
		}
	}
	return winner
}

func trackedNodeFieldAssertionPreferred(candidate, current trackedNodeFieldAssertion) bool {
	if comparison := compareClaims(candidate.claim, current.claim); comparison != 0 {
		return comparison < 0
	}
	if candidate.value != current.value {
		return candidate.value < current.value
	}
	if candidate.explicit != current.explicit {
		return !candidate.explicit
	}
	return false
}

func (history *nodeFieldHistory) hasConflict() bool {
	for _, assertion := range history.assertions[1:] {
		if assertion.value != history.assertions[0].value {
			return true
		}
	}
	return false
}

func (claims nodeFieldClaims) settle(g *core.Graph) {
	for id, fields := range claims {
		node, ok := g.Node(id)
		if !ok {
			continue
		}
		var best *core.Claim
		for _, field := range []string{"name", "type"} {
			winner := fields[field].winner()
			if winner.explicit && (best == nil || claimPreferred(winner.claim, best)) {
				best = &winner.claim
			}
		}
		node.Claim = cloneClaim(best)
	}
}

func cloneClaim(claim *core.Claim) *core.Claim {
	if claim == nil {
		return nil
	}
	copy := *claim
	return &copy
}

func (e *enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	report := &enrichers.Report{Enricher: e.Name()}
	ix := NewIndex(g)
	tallies := map[string]*tally{}
	nodeClaims := newNodeFieldClaims(g)
	edgeClaims := newEdgeAssertionTracker(g)

	for _, doc := range e.docs {
		if doc == nil {
			continue
		}
		report.Sources = append(report.Sources, doc.Source)
		if w := doc.window(); w != "" && report.Window == "" {
			report.Window = w
		}
		if err := e.applyDocument(g, ix, doc, tallies, nodeClaims, edgeClaims, report); err != nil {
			return nil, err
		}
		recordOverlay(g, doc)
	}

	e.settle(g, tallies)
	nodeClaims.settle(g)
	countCoverage(g, report)

	g.Normalize()
	report.Conflicts = len(g.Conflicts)
	report.Sort()

	if e.opts.Unmatched == PolicyError && !report.Clean() {
		return report, fmt.Errorf("overlay: %d unmatched and %d ambiguous subjects",
			len(report.Unmatched), len(report.Ambiguous))
	}
	return report, nil
}

func (e *enricher) applyDocument(g *core.Graph, ix *Index, doc *Document, tallies map[string]*tally, nodeClaims nodeFieldClaims, edgeClaims *edgeAssertionTracker, report *enrichers.Report) error {
	sinkIDs := map[string]string{}

	for _, a := range doc.Assertions {
		claim := doc.claim(a)

		switch a.Assert {
		case AssertNode:
			id, ok := e.subject(g, ix, doc, a, a.Subject, claim, report)
			if !ok {
				continue
			}
			applyNodeAssertion(g, id, a, claim, nodeClaims)
			report.Applied++

		case AssertLogsDeclared, AssertLogsObserved, AssertLogsNone:
			id, ok := e.subject(g, ix, doc, a, a.Subject, claim, report)
			if !ok {
				continue
			}
			sink := ""
			if a.Sink != "" {
				var err error
				sink, err = e.sinkNode(g, ix, doc, a.Sink, sinkIDs)
				if err != nil {
					return err
				}
			}
			applyLogAssertion(g, tallies, id, sink, a, claim, ix, edgeClaims)
			report.Applied++

		case AssertEdge, AssertEdgeSuppress:
			from, ok := e.subject(g, ix, doc, a, a.From, claim, report)
			if !ok {
				continue
			}
			to, ok := e.subject(g, ix, doc, a, a.To, claim, report)
			if !ok {
				continue
			}
			applyEdgeAssertion(g, from, to, a, claim, edgeClaims)
			report.Applied++
		}
	}
	return nil
}

// subject resolves a selector and applies the unmatched and ambiguous policies.
func (e *enricher) subject(g *core.Graph, ix *Index, doc *Document, a Assertion, sel Selector, claim core.Claim, report *enrichers.Report) (string, bool) {
	res := ix.Resolve(sel)

	switch {
	case res.ID != "":
		return res.ID, true

	case len(res.Candidates) > 1:
		// Ambiguity must never manufacture a finding. If this assertion were
		// merely skipped, a candidate with no other evidence would keep
		// whatever state it had and could end up drawn as a blind spot that
		// nobody ever claimed. Poisoning the candidates to unknown, with the
		// reason attached, is the only reading that stays honest about what
		// is and is not known.
		report.Ambiguous = append(report.Ambiguous, enrichers.Ambiguous{
			Selector:   sel.asMap(),
			Assert:     a.Assert,
			Candidates: res.Candidates,
		})
		for _, id := range res.Candidates {
			markUnknown(g, id, ambiguityReason(len(res.Candidates)))
		}
		return "", false

	default:
		reason := "no resource in this graph answers to it"
		if res.Stopped {
			reason = "an exact id was given and this graph has no such id"
		}
		if e.opts.Unmatched == PolicyAdopt {
			id := adopt(g, ix, sel, claim)
			report.Adopted = append(report.Adopted, id)
			report.Unmatched = append(report.Unmatched, enrichers.Unmatched{
				Selector: sel.asMap(), Assert: a.Assert, Reason: reason, Action: "adopted",
			})
			return id, true
		}
		report.Unmatched = append(report.Unmatched, enrichers.Unmatched{
			Selector: sel.asMap(), Assert: a.Assert, Reason: reason, Action: "dropped",
		})
		return "", false
	}
}

// ambiguityReason explains why a subject was left unknown.
//
// It reads correctly at two candidates, which is the commonest ambiguity there
// is. The text ends up in Coverage.Reason, so it is shown in the report, in a
// tooltip and in the detail panel — three places where "1 other subjects"
// would be read as carelessness about everything else too.
func ambiguityReason(candidates int) string {
	others := candidates - 1
	if others == 1 {
		return "an overlay assertion could not be told apart from one other subject"
	}
	return fmt.Sprintf("an overlay assertion could not be told apart from %d other subjects", others)
}

// adopt turns a selector that matched nothing into a node.
//
// Drawn with its own type so it can never be mistaken for parsed
// infrastructure, and named from the selector so the id is stable across runs
// — re-applying the same overlay has to produce the same graph.
func adopt(g *core.Graph, ix *Index, sel Selector, claim core.Claim) string {
	id := "asserted:" + sel.key()
	if ix.Has(id) {
		return id
	}

	c := claim
	if c.Note == "" {
		c.Note = "matched nothing in the graph; adopted so the assertion is not lost"
	}
	g.Nodes = append(g.Nodes, core.Node{
		ID:       id,
		Type:     "oekaki_asserted",
		Name:     sel.label(),
		Provider: "oekaki",
		Claim:    &c,
	})
	// Registered under the selector that produced it, not only under its name.
	// Without that, a second assertion about the same subject resolves to
	// nothing all over again: the node is not duplicated, but it is reported
	// as unmatched a second time, and the report is a file CI diffs.
	ix.Add(id, "oekaki_asserted", sel.label(), sel)
	return id
}

// sinkNode finds or creates the node a sink refers to.
//
// A destination that is already in the graph — a log group the IaC declares —
// is attached to rather than duplicated, so the diagram does not grow a second
// box for something it already had.
func (e *enricher) sinkNode(g *core.Graph, ix *Index, doc *Document, handle string, cache map[string]string) (string, error) {
	if id, ok := cache[handle]; ok {
		return id, nil
	}
	s, ok := doc.sink(handle)
	if !ok {
		return "", fmt.Errorf("%s: assertion names undeclared sink %q", doc.Source, handle)
	}

	// The schema lets a sink carry any type string, but only the registered
	// identity keys mean anything to the resolver. An unrecognised one — say
	// "object_prefix" — would otherwise match nothing and never fall through,
	// so a destination already in the graph would get a second box beside it.
	sel := Selector{"name": s.Name}
	if s.Type != "" && providers.IsSelectorKey(s.Type) {
		sel = Selector{s.Type: s.Name}
	}
	if res := ix.Resolve(sel); res.ID != "" {
		cache[handle] = res.ID
		return res.ID, nil
	}

	id := syntheticSinkID(s)
	if !ix.Has(id) {
		g.Nodes = append(g.Nodes, core.Node{
			ID:       id,
			Type:     "oekaki_log_sink",
			Name:     s.Name,
			Provider: "oekaki",
			Claim:    &core.Claim{Origin: core.OriginHuman, Note: "log destination named by an overlay"},
		})
		ix.Add(id, "oekaki_log_sink", s.Name, sel)
	}
	cache[handle] = id
	return id, nil
}

func syntheticSinkID(sink Sink) string {
	// Source is command-line provenance, not identity: the same bytes may be
	// addressed by a relative path, an absolute path, or standard input. Scope
	// the id to the complete sink declaration so those forms stay stable while
	// independently declared endpoints remain distinct.
	scope := sink.ID + "\x00" + sink.Type + "\x00" + sink.Name
	digest := sha256.Sum256([]byte(scope))
	return fmt.Sprintf("logsink:%x:%s", digest[:8], sink.ID)
}

func applyNodeAssertion(g *core.Graph, id string, a Assertion, claim core.Claim, claims nodeFieldClaims) {
	n, ok := g.Node(id)
	if !ok {
		return
	}
	fields := claims.forNode(g, id)

	if a.Type != "" {
		fields["type"].add(a.Type, claim)
		n.Type = fields["type"].winner().value
		recordNodeFieldHistory(g, id, "type", fields["type"])
	}
	if a.Name != "" {
		fields["name"].add(a.Name, claim)
		n.Name = fields["name"].winner().value
		recordNodeFieldHistory(g, id, "name", fields["name"])
	}
}

func recordNodeFieldHistory(g *core.Graph, id, field string, history *nodeFieldHistory) {
	if !history.hasConflict() {
		return
	}
	conflict := conflictFor(g, core.ConflictTargetEntity, id, field)
	for _, assertion := range history.assertions {
		appendClaimedValue(conflict, core.ClaimedValue{Value: assertion.value, Claim: assertion.claim})
	}
}

// applyLogAssertion records one piece of evidence and, where there is a sink,
// the edge that carries it.
//
// No new edge kind is needed for either half. A log driver naming a log group
// is literally a configuration reference, which is what iac_ref means; and
// "traffic was actually measured along this path" is the definition of
// observed. Three edge kinds overlaid is the point of the project, and they
// already say what is needed here.
func applyLogAssertion(g *core.Graph, tallies map[string]*tally, id, sink string, a Assertion, claim core.Claim, ix *Index, edges *edgeAssertionTracker) {
	t := tallies[id]
	if t == nil {
		t = &tally{}
		tallies[id] = t
	}

	ev := core.Evidence{
		Sink:    sink,
		Stream:  a.Stream,
		Via:     a.Via,
		Records: a.Records,
		Claim:   &claim,
	}

	switch a.Assert {
	case AssertLogsDeclared:
		ev.Kind = core.EvidenceDeclared
		t.declared = true
		if sink != "" {
			addEdge(g, id, sink, core.EdgeIACRef, claim, edges)
		}
	case AssertLogsObserved:
		ev.Kind = core.EvidenceObserved
		if a.Records != nil && *a.Records == 0 {
			t.looked = true
		} else {
			t.seen = true
			if sink != "" {
				addEdge(g, id, sink, core.EdgeObserved, claim, edges)
			}
		}
	case AssertLogsNone:
		ev.Kind = core.EvidenceNone
		t.looked = true
	}

	ev.Matched = matchedRule(ix, id)
	t.evidence = append(t.evidence, ev)
}

// matchedRule is best-effort provenance for how a subject was found. The
// resolver knows the rule; carrying it this far would thread a parameter
// through every call, so the common case is recorded and the rest left blank
// rather than guessed at.
func matchedRule(ix *Index, id string) string {
	if ix.Has(id) {
		return RuleID
	}
	return ""
}

type trackedEdgeAssertion struct {
	suppressed bool
	claim      core.Claim
	explicit   bool
}

type edgeAssertionHistory struct {
	index            int
	existedInitially bool
	assertions       []trackedEdgeAssertion
}

type edgeAssertionTracker struct {
	byKey map[string]*edgeAssertionHistory
}

func newEdgeAssertionTracker(g *core.Graph) *edgeAssertionTracker {
	tracker := &edgeAssertionTracker{byKey: map[string]*edgeAssertionHistory{}}
	for i := range g.Edges {
		edge := &g.Edges[i]
		key := core.EdgeKey(edge.From, edge.To, edge.Kind, edge.Relation)
		history := tracker.byKey[key]
		if history == nil {
			history = &edgeAssertionHistory{index: i, existedInitially: true}
			tracker.byKey[key] = history
		}
		history.add(trackedEdgeAssertion{
			suppressed: edge.Suppressed,
			claim:      claimOrParser(edge.Claim),
			explicit:   edge.Claim != nil,
		})
	}
	return tracker
}

func (tracker *edgeAssertionTracker) matching(g *core.Graph, from, to string, kind core.EdgeKind) *edgeAssertionHistory {
	for i := range g.Edges {
		edge := &g.Edges[i]
		if edge.From != from || edge.To != to || edge.Kind != kind {
			continue
		}
		key := core.EdgeKey(edge.From, edge.To, edge.Kind, edge.Relation)
		if history := tracker.byKey[key]; history != nil {
			return history
		}
	}
	return nil
}

func (tracker *edgeAssertionTracker) create(g *core.Graph, from, to string, kind core.EdgeKind) *edgeAssertionHistory {
	g.Edges = append(g.Edges, core.Edge{From: from, To: to, Kind: kind})
	history := &edgeAssertionHistory{index: len(g.Edges) - 1}
	tracker.byKey[core.EdgeKey(from, to, kind)] = history
	return history
}

func (history *edgeAssertionHistory) add(candidate trackedEdgeAssertion) {
	for _, existing := range history.assertions {
		if existing.suppressed == candidate.suppressed && existing.explicit == candidate.explicit && claimsEqual(existing.claim, candidate.claim) {
			return
		}
	}
	history.assertions = append(history.assertions, candidate)
}

func (history *edgeAssertionHistory) winner() trackedEdgeAssertion {
	winner := history.assertions[0]
	for _, candidate := range history.assertions[1:] {
		if trackedEdgeAssertionPreferred(candidate, winner) {
			winner = candidate
		}
	}
	return winner
}

func trackedEdgeAssertionPreferred(candidate, current trackedEdgeAssertion) bool {
	if candidate.suppressed != current.suppressed {
		return candidate.suppressed
	}
	if comparison := compareClaims(candidate.claim, current.claim); comparison != 0 {
		return comparison < 0
	}
	if candidate.explicit != current.explicit {
		return !candidate.explicit
	}
	return false
}

func (tracker *edgeAssertionTracker) apply(g *core.Graph, from, to string, kind core.EdgeKind, suppressed bool, claim core.Claim) {
	history := tracker.matching(g, from, to, kind)
	if history == nil {
		history = tracker.create(g, from, to, kind)
	}
	if suppressed && !history.existedInitially && claim.Note == "" {
		claim.Note = "asserted not to exist; no such edge was found"
	}
	history.add(trackedEdgeAssertion{suppressed: suppressed, claim: claim, explicit: true})

	winner := history.winner()
	edge := &g.Edges[history.index]
	edge.Suppressed = winner.suppressed
	if winner.explicit {
		edge.Claim = cloneClaim(&winner.claim)
	} else {
		edge.Claim = nil
	}
	recordEdgeAssertionHistory(g, edge, history)
}

func recordEdgeAssertionHistory(g *core.Graph, edge *core.Edge, history *edgeAssertionHistory) {
	var positive, suppressed bool
	for _, assertion := range history.assertions {
		if assertion.suppressed {
			suppressed = true
		} else {
			positive = true
		}
	}
	if !positive || !suppressed {
		return
	}
	conflict := conflictFor(g, core.ConflictTargetEdge, core.EdgeKey(edge.From, edge.To, edge.Kind, edge.Relation), "suppressed")
	for _, assertion := range history.assertions {
		appendClaimedValue(conflict, core.ClaimedValue{Value: boolString(assertion.suppressed), Claim: assertion.claim})
	}
}

func applyEdgeAssertion(g *core.Graph, from, to string, a Assertion, claim core.Claim, tracker *edgeAssertionTracker) {
	kind := a.Kind
	if kind == "" {
		kind = core.EdgeObserved
	}
	tracker.apply(g, from, to, kind, a.Assert == AssertEdgeSuppress, claim)
}

func addEdge(g *core.Graph, from, to string, kind core.EdgeKind, claim core.Claim, tracker *edgeAssertionTracker) {
	tracker.apply(g, from, to, kind, false, claim)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// settle turns each subject's accumulated evidence into a state.
func (e *enricher) settle(g *core.Graph, tallies map[string]*tally) {
	for _, id := range sortedTallies(tallies) {
		t := tallies[id]
		n, ok := g.Node(id)
		if !ok {
			continue
		}

		state, reason := decide(t)
		n.Coverage = &core.Coverage{State: state, Reason: reason, Evidence: t.evidence}

		if records := totalRecords(t.evidence); records != nil {
			if n.Metrics == nil {
				n.Metrics = map[string]float64{}
			}
			n.Metrics["log_records"] = *records
		}
	}
}

// decide is the whole state machine, and the reason it has five outcomes.
//
// Nothing here can produce a finding from silence: a subject nobody asserted
// anything about never reaches this function at all, and one that reaches it
// with neither a declaration nor a look comes out unknown. A blind spot is
// only ever something somebody went and checked.
func decide(t *tally) (core.CoverageState, string) {
	switch {
	case t.declared && t.seen:
		return core.CoverageFlowing, "declared, and logs were seen arriving"
	case t.declared && !t.seen:
		return core.CoverageSilent, "a destination is declared and nothing was seen arriving there"
	case !t.declared && t.seen:
		return core.CoverageUndeclared, "logs arrive from this, and nothing in the graph declares them"
	case t.looked:
		return core.CoverageBlind, "somebody looked and found no destination at all"
	default:
		return core.CoverageUnknown, "nothing conclusive was asserted"
	}
}

func totalRecords(ev []core.Evidence) *float64 {
	var sum float64
	var any bool
	for _, e := range ev {
		if e.Kind == core.EvidenceObserved && e.Records != nil {
			sum += *e.Records
			any = true
		}
	}
	if !any {
		return nil
	}
	return &sum
}

// markUnknown records that something is not known, without overwriting a
// state that was actually established.
func markUnknown(g *core.Graph, id, reason string) {
	n, ok := g.Node(id)
	if !ok {
		return
	}
	if n.Coverage != nil {
		return
	}
	n.Coverage = &core.Coverage{State: core.CoverageUnknown, Reason: reason}
}

func countCoverage(g *core.Graph, report *enrichers.Report) {
	counts := map[string]int{}
	for _, n := range g.Nodes {
		if n.Coverage == nil {
			continue
		}
		counts[string(n.Coverage.State)]++
	}
	if len(counts) > 0 {
		report.Coverage = counts
	}
}

func recordOverlay(g *core.Graph, doc *Document) {
	ref := core.OverlayRef{Source: doc.Source, Origin: core.OriginHuman}
	if doc.Metadata != nil {
		if doc.Metadata.Origin != "" {
			ref.Origin = doc.Metadata.Origin
		}
		ref.Author = doc.Metadata.Author
		ref.Window = doc.Metadata.Window
	}
	if g.Metadata == nil {
		g.Metadata = &core.Metadata{}
	}
	g.Metadata.Overlays = append(g.Metadata.Overlays, ref)
}

func conflictFor(g *core.Graph, targetKind core.ConflictTargetKind, target, field string) *core.Conflict {
	for i := range g.Conflicts {
		if g.Conflicts[i].TargetKind == targetKind && g.Conflicts[i].Target == target && g.Conflicts[i].Field == field {
			return &g.Conflicts[i]
		}
	}
	g.Conflicts = append(g.Conflicts, core.Conflict{TargetKind: targetKind, Target: target, Field: field})
	return &g.Conflicts[len(g.Conflicts)-1]
}

func appendClaimedValue(conflict *core.Conflict, candidate core.ClaimedValue) {
	for _, existing := range conflict.Claims {
		if existing.Value == candidate.Value && claimsEqual(existing.Claim, candidate.Claim) {
			return
		}
	}
	conflict.Claims = append(conflict.Claims, candidate)
	sort.SliceStable(conflict.Claims, func(i, j int) bool {
		a, b := conflict.Claims[i], conflict.Claims[j]
		if comparison := compareClaims(a.Claim, b.Claim); comparison != 0 {
			return comparison < 0
		}
		return a.Value < b.Value
	})
}

func claimsEqual(a, b core.Claim) bool {
	if a.Origin != b.Origin || a.Author != b.Author || a.Note != b.Note {
		return false
	}
	if a.Confidence == nil || b.Confidence == nil {
		return a.Confidence == nil && b.Confidence == nil
	}
	return *a.Confidence == *b.Confidence
}

func claimPreferred(candidate core.Claim, current *core.Claim) bool {
	return compareClaims(candidate, claimOrParser(current)) < 0
}

// compareClaims returns a negative value when a should be displayed instead
// of b. Rank is semantic; the remaining fields are canonical tie-breakers so
// equal-rank overlays never inherit command-line order.
func compareClaims(a, b core.Claim) int {
	if a.Origin.Rank() != b.Origin.Rank() {
		if a.Origin.Rank() > b.Origin.Rank() {
			return -1
		}
		return 1
	}
	if a.Author != b.Author {
		if a.Author < b.Author {
			return -1
		}
		return 1
	}
	if comparison := compareConfidence(a.Confidence, b.Confidence); comparison != 0 {
		return comparison
	}
	if a.Note < b.Note {
		return -1
	}
	if a.Note > b.Note {
		return 1
	}
	return 0
}

func compareConfidence(a, b *float64) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if *a < *b {
		return -1
	}
	if *a > *b {
		return 1
	}
	return 0
}

func claimOrParser(claim *core.Claim) core.Claim {
	if claim == nil {
		return core.Claim{Origin: core.OriginParser}
	}
	return *claim
}

func sortedTallies(t map[string]*tally) []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
