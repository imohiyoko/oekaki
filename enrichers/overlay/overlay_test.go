package overlay

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func graph() *core.Graph {
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}},
		Nodes: []core.Node{
			{ID: "aws_ecs_service.api", Type: "aws_ecs_service", Name: "api",
				Attrs: map[string]any{"name": "api"}},
			{ID: "aws_ecs_service.checkout", Type: "aws_ecs_service", Name: "checkout",
				Attrs: map[string]any{"name": "checkout"}},
			{ID: "aws_lb.api", Type: "aws_lb", Name: "api",
				Attrs: map[string]any{"name": "api"}},
			{ID: "aws_db_instance.orders", Type: "aws_db_instance", Name: "orders"},
			{ID: "aws_cloudwatch_log_group.app", Type: "aws_cloudwatch_log_group", Name: "app",
				Attrs: map[string]any{"name": "/platform/app"}},
			{ID: "kubernetes_deployment.shop", Type: "kubernetes_deployment", Name: "checkout",
				Attrs: map[string]any{"metadata": map[string]any{"name": "checkout", "namespace": "shop"}}},
			{ID: "kubernetes_deployment.other", Type: "kubernetes_deployment", Name: "checkout",
				Attrs: map[string]any{"metadata": map[string]any{"name": "checkout", "namespace": "warehouse"}}},
		},
		Edges: []core.Edge{
			{From: "aws_ecs_service.api", To: "aws_db_instance.orders", Kind: core.EdgeIACRef},
		},
	}
	g.Normalize()
	return g
}

func apply(t *testing.T, body string, opts Options) (*core.Graph, *reportish) {
	t.Helper()

	doc, err := Parse([]byte(body), "test.json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g := graph()
	r, err := New([]*Document{doc}, opts).Enrich(g)
	if err != nil && opts.Unmatched != PolicyError {
		t.Fatalf("Enrich: %v", err)
	}
	return g, &reportish{r}
}

type reportish struct{ r interface{ Clean() bool } }

func doc(assertions string) string {
	return `{"kind":"oekaki.overlay","version":"0.1",
	  "metadata":{"origin":"human","author":"operator","window":"last-7d"},
	  "sinks":[{"id":"sink.app","type":"log_group","name":"/platform/app"}],
	  "assertions":[` + assertions + `]}`
}

func coverageOf(t *testing.T, g *core.Graph, id string) *core.Coverage {
	t.Helper()
	n, ok := g.Node(id)
	if !ok {
		t.Fatalf("node %q is not in the graph", id)
	}
	return n.Coverage
}

// The four interesting states, each reached the way a real overlay would
// reach it.
func TestEveryCoverageStateIsReachable(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app"},
	  {"assert":"logs.observed","subject":{"service":"api"},"sink":"sink.app","records":100},
	  {"assert":"logs.declared","subject":{"service":"checkout"},"sink":"sink.app"},
	  {"assert":"logs.observed","subject":{"service":"checkout"},"sink":"sink.app","records":0},
	  {"assert":"logs.none","subject":{"node":"aws_db_instance.orders"}},
	  {"assert":"logs.observed","subject":{"service":"nowhere"},"sink":"sink.app","records":5}`),
		Options{})

	for id, want := range map[string]core.CoverageState{
		"aws_ecs_service.api":      core.CoverageFlowing,
		"aws_ecs_service.checkout": core.CoverageSilent,
		"aws_db_instance.orders":   core.CoverageBlind,
		"asserted:service=nowhere": core.CoverageUndeclared,
	} {
		if got := coverageOf(t, g, id).State; got != want {
			t.Errorf("%s is %q, want %q", id, got, want)
		}
	}
}

// An observation that counted zero is somebody reporting an empty
// destination. Reading it as "logs were seen" would turn the finding into its
// opposite.
func TestZeroRecordsIsNotFlowing(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app"},
	  {"assert":"logs.observed","subject":{"service":"api"},"sink":"sink.app","records":0}`),
		Options{})

	if got := coverageOf(t, g, "aws_ecs_service.api").State; got != core.CoverageSilent {
		t.Errorf("state is %q, want %q", got, core.CoverageSilent)
	}
}

// The rule the whole design rests on: nobody having looked must never render
// as a finding.
func TestAbsenceIsNeverBlind(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"logs.none","subject":{"service":"api"}}`), Options{})

	if c := coverageOf(t, g, "aws_ecs_service.checkout"); c != nil {
		t.Errorf("a node nobody asserted anything about has coverage %q", c.State)
	}
}

// Ambiguity must not manufacture a finding either. If the assertion were
// merely skipped, a candidate with no other evidence could still be drawn as
// a blind spot that nobody ever claimed.
func TestAmbiguousSubjectIsNotAppliedAndNeverBlind(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"logs.none","subject":{"name":"api"}}`), Options{})

	for _, id := range []string{"aws_ecs_service.api", "aws_lb.api"} {
		c := coverageOf(t, g, id)
		if c == nil {
			t.Fatalf("%s has no coverage; the ambiguity was not recorded", id)
		}
		if c.State == core.CoverageBlind {
			t.Errorf("%s was painted blind by an assertion that could not be told apart", id)
		}
		if c.State != core.CoverageUnknown {
			t.Errorf("%s is %q, want %q", id, c.State, core.CoverageUnknown)
		}
	}
}

// An exact id that misses stops the ladder. Falling through to a name match
// would silently attach the assertion to a different resource.
func TestAnExactIDThatMissesDoesNotFallThrough(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"logs.none","subject":{"node":"api"}}`), Options{})

	if c := coverageOf(t, g, "aws_ecs_service.api"); c != nil {
		t.Errorf("a missed id was rescued by a name match: api is %q", c.State)
	}
}

// Identity keys are ANDed: the same workload name in another namespace is a
// different workload.
func TestIdentityKeysAreANDed(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"logs.none","subject":{"workload":"checkout","namespace":"shop"}}`),
		Options{})

	if c := coverageOf(t, g, "kubernetes_deployment.shop"); c == nil || c.State != core.CoverageBlind {
		t.Errorf("the workload in shop was not the one that matched")
	}
	if c := coverageOf(t, g, "kubernetes_deployment.other"); c != nil {
		t.Errorf("the workload in warehouse was matched too: %q", c.State)
	}
}

func TestUnmatchedIsAdoptedByDefault(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"logs.observed","subject":{"service":"ghost"},"sink":"sink.app","records":3}`),
		Options{})

	n, ok := g.Node("asserted:service=ghost")
	if !ok {
		t.Fatal("an observed log stream that matched nothing was dropped")
	}
	if n.Type != "oekaki_asserted" {
		t.Errorf("adopted node has type %q; it must not look like parsed infrastructure", n.Type)
	}
	if n.Claim == nil || n.Claim.Origin != core.OriginHuman {
		t.Error("the adopted node does not record who asserted it")
	}
}

func TestReportPolicyDropsInsteadOfAdopting(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"logs.observed","subject":{"service":"ghost"},"sink":"sink.app","records":3}`),
		Options{Unmatched: PolicyReport})

	if _, ok := g.Node("asserted:service=ghost"); ok {
		t.Error("report policy adopted a node anyway")
	}
}

func TestErrorPolicyFails(t *testing.T) {
	d, err := Parse([]byte(doc(`{"assert":"logs.none","subject":{"service":"ghost"}}`)), "test.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New([]*Document{d}, Options{Unmatched: PolicyError}).Enrich(graph()); err == nil {
		t.Error("error policy accepted an unmatched subject")
	}
}

// Suppression records a disagreement; it does not erase the thing disagreed
// about, because a reader who cannot see the edge cannot judge the claim.
func TestSuppressionKeepsTheEdgeAndRecordsTheConflict(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"edge.suppress","from":{"node":"aws_ecs_service.api"},"to":{"node":"aws_db_instance.orders"},"kind":"iac_ref"}`),
		Options{})

	var found bool
	for _, e := range g.Edges {
		if e.From == "aws_ecs_service.api" && e.To == "aws_db_instance.orders" && e.Kind == core.EdgeIACRef {
			found = true
			if !e.Suppressed {
				t.Error("the edge was not marked suppressed")
			}
		}
	}
	if !found {
		t.Error("the suppressed edge was deleted rather than flagged")
	}
	if len(g.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(g.Conflicts))
	}
	if got := g.Conflicts[0].TargetKind; got != core.ConflictTargetEdge {
		t.Errorf("conflict target kind = %q, want %q", got, core.ConflictTargetEdge)
	}
}

func TestNodeConflictTargetsEntity(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"node","subject":{"node":"aws_lb.api"},"type":"aws_lb","name":"api-public"}`),
		Options{})

	if len(g.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(g.Conflicts))
	}
	conflict := g.Conflicts[0]
	if conflict.TargetKind != core.ConflictTargetEntity || conflict.Target != "aws_lb.api" {
		t.Errorf("conflict target = (%q, %q), want (%q, %q)",
			conflict.TargetKind, conflict.Target, core.ConflictTargetEntity, "aws_lb.api")
	}
}

// Renaming is the common case for a reader with a diagram in front of them:
// the parser found the resource, the label it derived is not what anyone calls
// it, and stating the type again to be allowed to fix the label would be
// asserting something the input already said.
func TestANodeCanBeRenamedWithoutRestatingItsType(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"node","subject":{"node":"aws_lb.api"},"name":"api-public"}`),
		Options{})

	n, ok := g.Node("aws_lb.api")
	if !ok {
		t.Fatal("the renamed node is gone")
	}
	if n.Name != "api-public" {
		t.Errorf("name = %q, want %q", n.Name, "api-public")
	}
	if n.Type != "aws_lb" {
		t.Errorf("type = %q, want the parsed type %q", n.Type, "aws_lb")
	}
}

// An assertion may add what no parser found. That is the point of the format.
func TestAnEdgeNobodyDeclaredCanBeAsserted(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"edge","from":{"service":"checkout"},"to":{"node":"aws_db_instance.orders"},
	   "kind":"observed","origin":"ai","confidence":0.6}`),
		Options{})

	for _, e := range g.Edges {
		if e.From == "aws_ecs_service.checkout" && e.To == "aws_db_instance.orders" {
			if e.Claim == nil || e.Claim.Origin != core.OriginAI {
				t.Fatal("the asserted edge does not record that a model claimed it")
			}
			if e.Claim.Confidence == nil || *e.Claim.Confidence != 0.6 {
				t.Error("the confidence was lost")
			}
			return
		}
	}
	t.Error("the asserted edge was not added")
}

func TestSuppressingAMissingEdgeExplainsTheSyntheticAssertion(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"edge.suppress","from":{"service":"checkout"},"to":{"node":"aws_db_instance.orders"},"kind":"observed"}`), Options{})
	for _, edge := range g.Edges {
		if edge.From == "aws_ecs_service.checkout" && edge.To == "aws_db_instance.orders" && edge.Kind == core.EdgeObserved {
			if !edge.Suppressed || edge.Claim == nil || !strings.Contains(edge.Claim.Note, "no such edge was found") {
				t.Fatalf("missing-edge suppression lost its explanation: %#v", edge)
			}
			return
		}
	}
	t.Fatal("missing edge suppression was not retained")
}

// A log destination already in the graph is attached to, not duplicated.
func TestASinkThatExistsIsNotDuplicated(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app"}`), Options{})

	if _, ok := g.Node("logsink:sink.app"); ok {
		t.Error("a second node was synthesised for a log group already in the graph")
	}
	for _, e := range g.Edges {
		if e.From == "aws_ecs_service.api" && e.To == "aws_cloudwatch_log_group.app" && e.Kind == core.EdgeIACRef {
			return
		}
	}
	t.Error("no iac_ref edge was drawn to the existing log group")
}

// No fourth edge kind: declared is a configuration reference and observed is
// measured traffic, which is what the two existing kinds already mean.
func TestLogAssertionsUseTheExistingEdgeKinds(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app"},
	  {"assert":"logs.observed","subject":{"service":"api"},"sink":"sink.app","records":9}`),
		Options{})

	kinds := map[core.EdgeKind]bool{}
	for _, e := range g.Edges {
		if e.To == "aws_cloudwatch_log_group.app" {
			kinds[e.Kind] = true
		}
	}
	if !kinds[core.EdgeIACRef] || !kinds[core.EdgeObserved] {
		t.Errorf("log edges are %v, want both iac_ref and observed", kinds)
	}
	for k := range kinds {
		if !k.Valid() {
			t.Errorf("a new edge kind %q appeared", k)
		}
	}
}

func TestEnrichedGraphValidates(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app"},
	  {"assert":"logs.observed","subject":{"service":"api"},"sink":"sink.app","records":100},
	  {"assert":"logs.none","subject":{"service":"checkout"}},
	  {"assert":"logs.observed","subject":{"service":"ghost"},"sink":"sink.app","records":1},
	  {"assert":"edge.suppress","from":{"node":"aws_ecs_service.api"},"to":{"node":"aws_db_instance.orders"},"kind":"iac_ref"}`),
		Options{})

	if err := g.Validate(); err != nil {
		t.Errorf("the enriched graph does not validate: %v", err)
	}
}

func TestEnrichIsDeterministic(t *testing.T) {
	body := doc(`
	  {"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app"},
	  {"assert":"logs.observed","subject":{"service":"api"},"sink":"sink.app","records":100},
	  {"assert":"logs.none","subject":{"name":"api"}},
	  {"assert":"logs.observed","subject":{"service":"ghost"},"sink":"sink.app","records":1}`)

	var first string
	for i := range 10 {
		g, _ := apply(t, body, Options{})
		out, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(out)
			continue
		}
		if string(out) != first {
			t.Fatal("two runs over the same overlay produced different graphs")
		}
	}
}

// The error a generator sees has to name the key and list the alternatives,
// or the model cannot correct itself.
func TestUnknownSelectorKeyIsRejectedWithTheKnownOnes(t *testing.T) {
	_, err := Parse([]byte(doc(`{"assert":"logs.none","subject":{"svc":"api"}}`)), "test.json")
	if err == nil {
		t.Fatal("an unknown selector key was accepted")
	}
	for _, want := range []string{"svc", "service", "workload"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// A flat struct with a discriminator buys generation reliability at the cost
// of type safety. Validate is where that cost is paid back.
func TestFieldNotMeaningfulForTheAssertIsRejected(t *testing.T) {
	_, err := Parse([]byte(doc(`{"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app","from":{"name":"api"}}`)), "test.json")
	if err == nil {
		t.Fatal(`a "from" on a logs.declared was accepted`)
	}
	if !strings.Contains(err.Error(), "not meaningful") {
		t.Errorf("the error does not say why: %v", err)
	}
}

func TestUndeclaredSinkIsRejected(t *testing.T) {
	_, err := Parse([]byte(doc(`{"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.nope"}`)), "test.json")
	if err == nil {
		t.Fatal("an assertion naming an undeclared sink was accepted")
	}
}

// A namespace alone selects the namespace, never the workloads in it. Left to
// the resolver it would come out as a confusing ambiguity instead of a clear
// mistake.
func TestNamespaceAloneIsRejected(t *testing.T) {
	_, err := Parse([]byte(doc(`{"assert":"logs.none","subject":{"namespace":"shop"}}`)), "test.json")
	if err == nil {
		t.Fatal("a namespace-only selector was accepted")
	}
}

func TestOverlayIsRecordedInMetadata(t *testing.T) {
	g, _ := apply(t, doc(`{"assert":"logs.none","subject":{"service":"api"}}`), Options{})

	if g.Metadata == nil || len(g.Metadata.Overlays) != 1 {
		t.Fatal("the graph does not record that an overlay was applied")
	}
	if g.Metadata.Overlays[0].Window != "last-7d" {
		t.Error("the author's window caption was lost, so the diagram cannot say what period it covers")
	}
}

// A second assertion about a subject the first one adopted has to land on that
// node, not be reported unmatched all over again. The report is a file CI
// diffs, so a duplicate there is a diff nobody can explain.
func TestASecondAssertionFindsWhatTheFirstAdopted(t *testing.T) {
	d, err := Parse([]byte(doc(`
	  {"assert":"logs.observed","subject":{"service":"ghost"},"sink":"sink.app","records":5},
	  {"assert":"logs.declared","subject":{"service":"ghost"},"sink":"sink.app"}`)), "test.json")
	if err != nil {
		t.Fatal(err)
	}

	g := graph()
	r, err := New([]*Document{d}, Options{}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}

	if len(r.Unmatched) != 1 {
		t.Errorf("one subject was reported unmatched %d times", len(r.Unmatched))
	}
	if len(r.Adopted) != 1 {
		t.Errorf("one subject was adopted %d times", len(r.Adopted))
	}
	// Both assertions landed, so the state reflects both of them.
	if c := coverageOf(t, g, "asserted:service=ghost"); c == nil || c.State != core.CoverageFlowing {
		t.Errorf("the second assertion did not reach the adopted node: %+v", c)
	}
}

// "1 other subjects" reads as carelessness, and two candidates is the
// commonest ambiguity there is.
func TestAmbiguityReasonReadsCorrectlyAtTwo(t *testing.T) {
	if got := ambiguityReason(2); !strings.Contains(got, "one other subject") || strings.Contains(got, "subjects") {
		t.Errorf("ambiguityReason(2) = %q", got)
	}
	if got := ambiguityReason(4); !strings.Contains(got, "3 other subjects") {
		t.Errorf("ambiguityReason(4) = %q", got)
	}
}

// The schema lets a sink carry any type string, but only registered identity
// keys mean anything to the resolver. An unrecognised one must still find a
// destination the graph already has rather than growing a second box beside it.
func TestASinkWithAnUnknownTypeStillMatchesByName(t *testing.T) {
	body := `{"kind":"oekaki.overlay","version":"0.1",
	  "metadata":{"origin":"human"},
	  "sinks":[{"id":"sink.app","type":"object_prefix","name":"app"}],
	  "assertions":[{"assert":"logs.declared","subject":{"service":"api"},"sink":"sink.app"}]}`

	d, err := Parse([]byte(body), "test.json")
	if err != nil {
		t.Fatal(err)
	}
	g := graph()
	if _, err := New([]*Document{d}, Options{}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	if _, ok := g.Node("logsink:sink.app"); ok {
		t.Error("a second node was synthesised for a destination already in the graph")
	}
	for _, e := range g.Edges {
		if e.From == "aws_ecs_service.api" && e.To == "aws_cloudwatch_log_group.app" {
			return
		}
	}
	t.Error("no edge was drawn to the existing destination")
}
