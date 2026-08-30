package core

import (
	"math"
	"strings"
	"testing"
)

func conf(v float64) *float64 { return &v }

func coverageFixture(c *Coverage) *Graph {
	g := &Graph{
		Version: Version,
		Axes:    []Axis{{ID: AxisNetwork}},
		Nodes: []Node{
			{ID: "logsink:app", Type: "oekaki_log_sink", Name: "app"},
			{ID: "aws_ecs_service.api", Type: "aws_ecs_service", Name: "api", Coverage: c},
		},
	}
	g.Normalize()
	return g
}

// A coverage state is a finding. A finding with nothing behind it cannot be
// argued with, and a coverage map whose findings cannot be argued with is not
// worth having.
func TestCoverageStateMustHaveEvidence(t *testing.T) {
	for _, state := range []CoverageState{CoverageFlowing, CoverageSilent, CoverageUndeclared} {
		g := coverageFixture(&Coverage{State: state})
		if err := g.Validate(); err == nil {
			t.Errorf("state %q was accepted with no evidence behind it", state)
		}
	}
}

// "Blind" is the one state that means somebody went and looked. Without a
// "none" evidence it is indistinguishable from nobody having checked, and
// painting an unchecked resource as a blind spot is a fabricated finding.
func TestBlindRequiresSomebodyToHaveLooked(t *testing.T) {
	g := coverageFixture(&Coverage{
		State:    CoverageBlind,
		Evidence: []Evidence{{Kind: EvidenceObserved, Sink: "logsink:app"}},
	})
	if err := g.Validate(); err == nil {
		t.Error("blind was accepted without a none evidence")
	}

	g = coverageFixture(&Coverage{
		State:    CoverageBlind,
		Evidence: []Evidence{{Kind: EvidenceNone}},
	})
	if err := g.Validate(); err != nil {
		t.Errorf("blind with a none evidence was rejected: %v", err)
	}
}

// Unknown means nobody has said anything. Evidence attached to it is a
// contradiction, and usually a sign that a state was cleared without its
// basis being cleared with it.
func TestUnknownCarriesNoEvidence(t *testing.T) {
	g := coverageFixture(&Coverage{
		State:    CoverageUnknown,
		Evidence: []Evidence{{Kind: EvidenceObserved, Sink: "logsink:app"}},
	})
	if err := g.Validate(); err == nil {
		t.Error("unknown was accepted while carrying evidence")
	}
}

func TestEvidenceSinkMustExist(t *testing.T) {
	g := coverageFixture(&Coverage{
		State:    CoverageFlowing,
		Evidence: []Evidence{{Kind: EvidenceObserved, Sink: "logsink:nowhere"}},
	})
	if err := g.Validate(); err == nil {
		t.Error("evidence naming a sink that is not in the graph was accepted")
	}
}

func TestUnknownOriginIsRejected(t *testing.T) {
	g := coverageFixture(nil)
	g.Nodes[0].Claim = &Claim{Origin: "vibes"}
	if err := g.Validate(); err == nil {
		t.Error("a claim with an unknown origin was accepted")
	}
}

func TestConfidenceMustBeAProbability(t *testing.T) {
	for _, c := range []float64{-0.1, 1.5, math.NaN()} {
		g := coverageFixture(nil)
		g.Nodes[0].Claim = &Claim{Origin: OriginAI, Confidence: conf(c)}
		if err := g.Validate(); err == nil {
			t.Errorf("confidence %v was accepted", c)
		}
	}
}

// Absent means the parser said so. Reading it any other way would make every
// parser-derived graph look like it carried assertions.
func TestAbsentClaimReadsAsParser(t *testing.T) {
	if got := claimOrParser(nil).Origin; got != OriginParser {
		t.Errorf("absent claim read as %q, want %q", got, OriginParser)
	}
}

func TestOriginRankIsATotalOrder(t *testing.T) {
	if OriginHuman.Rank() <= OriginAI.Rank() || OriginAI.Rank() <= OriginParser.Rank() {
		t.Error("human > ai > parser does not hold")
	}
}

func twoNodeGraph(edges ...Edge) *Graph {
	return &Graph{
		Version: Version,
		Axes:    []Axis{{ID: AxisNetwork}},
		Nodes: []Node{
			{ID: "a", Type: "aws_instance", Name: "a"},
			{ID: "b", Type: "aws_instance", Name: "b"},
		},
		Edges: edges,
	}
}

// Two sources both finding an edge agree. Only a disagreement about whether
// it is real is a conflict, and that one has to survive into the document
// rather than being resolved into silence.
func TestDedupeMergesSuppressionAndRecordsTheDisagreement(t *testing.T) {
	g := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: &Claim{Origin: OriginHuman}},
	)
	g.Normalize()

	if len(g.Edges) != 1 {
		t.Fatalf("got %d edges, want 1 after dedupe", len(g.Edges))
	}
	if !g.Edges[0].Suppressed {
		t.Error("suppression was lost when the duplicate was merged")
	}
	if g.Edges[0].Claim == nil || g.Edges[0].Claim.Origin != OriginHuman {
		t.Error("the higher-ranked claim did not survive the merge")
	}
	if len(g.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(g.Conflicts))
	}
	if g.Conflicts[0].Target != EdgeKey("a", "b", EdgeIACRef) {
		t.Errorf("conflict target is %q", g.Conflicts[0].Target)
	}
	if err := g.Validate(); err != nil {
		t.Errorf("the merged graph does not validate: %v", err)
	}
}

// Agreement is not conflict: if both sources say the edge is real there is
// nothing for a reader to adjudicate, and an entry would be noise.
func TestAgreeingDuplicatesRecordNoConflict(t *testing.T) {
	g := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Claim: &Claim{Origin: OriginHuman}},
	)
	g.Normalize()

	if len(g.Conflicts) != 0 {
		t.Errorf("two sources agreeing produced %d conflicts", len(g.Conflicts))
	}
}

// Merging must not depend on which duplicate was seen first, or the same two
// overlays would produce different bytes depending on the command line.
func TestMergeIsOrderIndependent(t *testing.T) {
	forward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: &Claim{Origin: OriginHuman}},
	)
	backward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: &Claim{Origin: OriginHuman}},
		Edge{From: "a", To: "b", Kind: EdgeIACRef},
	)
	forward.Normalize()
	backward.Normalize()

	a, err := forward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	b, err := backward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("merge order changed the output:\n%s\n---\n%s", a, b)
	}
}

func TestDedupeMergesAttrsWithoutDependingOnArrivalOrder(t *testing.T) {
	forward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Attrs: map[string]any{"left": "kept", "shared": "z"}},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Attrs: map[string]any{"right": "kept", "shared": "a"}},
	)
	backward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Attrs: map[string]any{"right": "kept", "shared": "a"}},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Attrs: map[string]any{"left": "kept", "shared": "z"}},
	)

	forward.Normalize()
	backward.Normalize()
	a, err := forward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	b, err := backward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("attribute merge depends on arrival order:\n%s\n---\n%s", a, b)
	}
	attrs := forward.Edges[0].Attrs
	if attrs["left"] != "kept" || attrs["right"] != "kept" || attrs["shared"] != "a" {
		t.Fatalf("merged attrs = %#v", attrs)
	}
}

func TestSuppressionIsFailSafeAcrossClaimRanks(t *testing.T) {
	human := &Claim{Origin: OriginHuman, Author: "operator"}
	ai := &Claim{Origin: OriginAI, Author: "assistant"}
	forward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: ai},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: false, Claim: human},
	)
	backward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: false, Claim: human},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: ai},
	)

	forward.Normalize()
	backward.Normalize()
	if !forward.Edges[0].Suppressed {
		t.Fatal("higher-ranked positive assertion cleared a suppression")
	}
	if got := forward.Edges[0].Claim; got == nil || got.Origin != OriginAI {
		t.Fatalf("effective suppression claim = %#v, want AI", got)
	}
	if len(forward.Conflicts) != 1 || forward.Conflicts[0].Claims[0].Value != "true" || forward.Conflicts[0].Claims[0].Claim.Origin != OriginAI {
		t.Fatalf("conflict does not put the displayed assertion first: %#v", forward.Conflicts)
	}
	a, err := forward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	b, err := backward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("suppression merge depends on arrival order:\n%s\n---\n%s", a, b)
	}
}

func TestEqualRankSuppressionUsesCanonicalClaimTieBreak(t *testing.T) {
	alice := &Claim{Origin: OriginHuman, Author: "alice", Note: "reviewed first"}
	bob := &Claim{Origin: OriginHuman, Author: "bob", Note: "reviewed second"}
	forward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Claim: bob},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: alice},
	)
	backward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: alice},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Claim: bob},
	)

	forward.Normalize()
	backward.Normalize()
	if !forward.Edges[0].Suppressed || forward.Edges[0].Claim.Author != "alice" {
		t.Fatalf("canonical equal-rank winner = %#v", forward.Edges[0])
	}
	a, err := forward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	b, err := backward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("equal-rank merge depends on arrival order:\n%s\n---\n%s", a, b)
	}
}

func TestSuppressionConflictAggregatesAllAssertions(t *testing.T) {
	confidence := 0.9
	assertions := []Edge{
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Claim: &Claim{Origin: OriginHuman, Author: "alice"}},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: &Claim{Origin: OriginAI, Author: "bot", Confidence: &confidence}},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: &Claim{Origin: OriginHuman, Author: "bob"}},
	}
	orders := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	var canonical []byte
	for _, order := range orders {
		g := twoNodeGraph(assertions[order[0]], assertions[order[1]], assertions[order[2]])
		g.Normalize()
		if len(g.Conflicts) != 1 {
			t.Fatalf("order %v: got %d pairwise conflicts, want one aggregate: %#v", order, len(g.Conflicts), g.Conflicts)
		}
		if got := len(g.Conflicts[0].Claims); got != 3 {
			t.Fatalf("order %v: aggregate has %d claims, want 3: %#v", order, got, g.Conflicts[0])
		}
		if g.Conflicts[0].Claims[0].Value != boolValue(g.Edges[0].Suppressed) {
			t.Fatalf("order %v: displayed edge and first conflict claim disagree: edge=%t conflict=%q", order, g.Edges[0].Suppressed, g.Conflicts[0].Claims[0].Value)
		}
		first, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		g.Normalize()
		second, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(second) {
			t.Fatalf("order %v: normalizing the aggregate twice changed the output:\n%s\n---\n%s", order, first, second)
		}
		if canonical == nil {
			canonical = first
		} else if string(first) != string(canonical) {
			t.Fatalf("order %v changed aggregate output:\n%s\n---\n%s", order, first, canonical)
		}
	}
}

func TestConflictAggregationCombinesExistingAndGeneratedClaims(t *testing.T) {
	g := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true, Claim: &Claim{Origin: OriginHuman, Author: "alice"}},
	)
	g.Conflicts = []Conflict{{
		TargetKind: ConflictTargetEdge,
		Target:     EdgeKey("a", "b", EdgeIACRef),
		Field:      "suppressed",
		Claims: []ClaimedValue{
			{Value: "false", Claim: Claim{Origin: OriginParser}},
			{Value: "true", Claim: Claim{Origin: OriginAI, Author: "reviewer"}},
		},
	}}

	g.Normalize()
	if len(g.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want one aggregate: %#v", len(g.Conflicts), g.Conflicts)
	}
	if got := len(g.Conflicts[0].Claims); got != 3 {
		t.Fatalf("aggregate has %d distinct claims, want 3: %#v", got, g.Conflicts[0])
	}
}

func TestNormalizeDropsConflictWhoseClaimsCollapseToAgreement(t *testing.T) {
	claim := ClaimedValue{Value: "api", Claim: Claim{Origin: OriginHuman, Author: "operator"}}
	g := twoNodeGraph()
	g.Conflicts = []Conflict{{
		TargetKind: ConflictTargetEntity,
		Target:     "a",
		Field:      "name",
		Claims:     []ClaimedValue{claim, claim},
	}}

	g.Normalize()
	if len(g.Conflicts) != 0 {
		t.Fatalf("duplicate agreement remained a conflict: %#v", g.Conflicts)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("normalized graph became invalid: %v", err)
	}
}

func TestExplicitParserClaimCannotClearSuppression(t *testing.T) {
	forward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Claim: &Claim{Origin: OriginParser}},
	)
	backward := twoNodeGraph(
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Claim: &Claim{Origin: OriginParser}},
		Edge{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true},
	)

	forward.Normalize()
	backward.Normalize()
	if !forward.Edges[0].Suppressed {
		t.Fatal("explicit empty parser claim cleared suppression")
	}
	if got := forward.Conflicts[0].Claims[0].Value; got != "true" {
		t.Fatalf("first conflict value = %q, want displayed true", got)
	}
	a, err := forward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	b, err := backward.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("explicit parser claim merge depends on order:\n%s\n---\n%s", a, b)
	}
}

func TestUnclaimedDuplicateCannotClearSuppression(t *testing.T) {
	for _, edges := range [][]Edge{
		{
			{From: "a", To: "b", Kind: EdgeIACRef},
			{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true},
		},
		{
			{From: "a", To: "b", Kind: EdgeIACRef, Suppressed: true},
			{From: "a", To: "b", Kind: EdgeIACRef},
		},
	} {
		g := twoNodeGraph(edges...)
		g.Normalize()
		if len(g.Edges) != 1 || !g.Edges[0].Suppressed {
			t.Fatalf("duplicate normalized to %#v, want one suppressed edge", g.Edges)
		}
	}
}

func TestNormalizeSortsEvidence(t *testing.T) {
	g := coverageFixture(&Coverage{
		State: CoverageFlowing,
		Evidence: []Evidence{
			{Kind: EvidenceObserved, Sink: "logsink:app"},
			{Kind: EvidenceDeclared, Sink: "logsink:app", Stream: "b"},
			{Kind: EvidenceDeclared, Sink: "logsink:app", Stream: "a"},
		},
	})

	api, ok := g.Node("aws_ecs_service.api")
	if !ok {
		t.Fatal("fixture lost its node")
	}
	var got []string
	for _, e := range api.Coverage.Evidence {
		got = append(got, e.Kind+"/"+e.Stream)
	}
	want := []string{"declared/a", "declared/b", "observed/"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("evidence order is %v, want %v", got, want)
	}
}

// Two pieces of evidence that agree on every field naming a sink can still be
// different evidence: found by a different rule, or claimed by a different
// person. A sort key that stops before those fields leaves such a pair tied,
// and a stable sort then keeps whichever order the overlays happened to be
// read in — which is a command line, not a fact about the estate.
//
// This is the pair the order-invariance test in enrichers/overlay does not
// happen to produce, so it is asserted here directly, on both fields.
func TestEvidenceOrderDoesNotDependOnArrivalOrder(t *testing.T) {
	sure, unsure := 0.9, 0.4

	for _, tc := range []struct {
		name string
		a, b Evidence
	}{
		{
			name: "differing only in which rule matched",
			a:    Evidence{Kind: EvidenceObserved, Sink: "logsink:app", Matched: "id"},
			b:    Evidence{Kind: EvidenceObserved, Sink: "logsink:app", Matched: "name"},
		},
		{
			name: "differing only in who claimed it",
			a:    Evidence{Kind: EvidenceObserved, Sink: "logsink:app", Claim: &Claim{Origin: OriginHuman, Author: "operator"}},
			b:    Evidence{Kind: EvidenceObserved, Sink: "logsink:app", Claim: &Claim{Origin: OriginHuman, Author: "reviewer"}},
		},
		{
			name: "differing only in how sure the claimant was",
			a:    Evidence{Kind: EvidenceObserved, Sink: "logsink:app", Claim: &Claim{Origin: OriginAI, Confidence: &sure}},
			b:    Evidence{Kind: EvidenceObserved, Sink: "logsink:app", Claim: &Claim{Origin: OriginAI, Confidence: &unsure}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forward := coverageFixture(&Coverage{State: CoverageFlowing, Evidence: []Evidence{tc.a, tc.b}})
			backward := coverageFixture(&Coverage{State: CoverageFlowing, Evidence: []Evidence{tc.b, tc.a}})

			a, err := forward.MarshalIndent()
			if err != nil {
				t.Fatal(err)
			}
			b, err := backward.MarshalIndent()
			if err != nil {
				t.Fatal(err)
			}
			if string(a) != string(b) {
				t.Errorf("the order the evidence arrived in reached the output:\n%s\n---\n%s", a, b)
			}
		})
	}
}

func TestConflictTargetMustExist(t *testing.T) {
	g := coverageFixture(nil)
	g.Conflicts = []Conflict{{
		TargetKind: ConflictTargetEntity,
		Target:     "nothing.here",
		Field:      "name",
		Claims: []ClaimedValue{
			{Value: "a", Claim: Claim{Origin: OriginHuman}},
			{Value: "b", Claim: Claim{Origin: OriginParser}},
		},
	}}
	if err := g.Validate(); err == nil {
		t.Error("a conflict about something not in the graph was accepted")
	}
}

// The version error is the first thing a user with an old graph sees, so it
// has to say what to do rather than only what is wrong.
func TestVersionMismatchSaysWhatToDo(t *testing.T) {
	g := coverageFixture(nil)
	g.Version = "0.2"

	err := g.Validate()
	if err == nil {
		t.Fatal("an old version was accepted")
	}
	if !strings.Contains(err.Error(), "regenerate") {
		t.Errorf("the version error does not say what to do: %v", err)
	}
}
