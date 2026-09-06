package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// Either end of an edge may be a container. A lift table that only knows about
// nodes drops those lines entirely — a fold nobody asked for, of a line nothing
// folded.
func TestALineToAContainerSurvivesFolding(t *testing.T) {
	g := crowded()
	g.Edges = append(g.Edges, core.Edge{From: "svc:api", To: "ns:shop", Kind: core.EdgeIACRef, Relation: "reads"})
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	found := false
	for _, e := range out.Edges {
		if e.From == "svc:api" && e.To == "ns:shop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the line to the container was deleted by folding: %#v", out.Edges)
	}
}

// Absent coverage means nobody knows, so dropping it turns a box everybody has
// looked at into one nobody has — in the one kind of drawing whose whole
// subject is which is which.
func TestAFoldKeepsTheCoverageItsMembersAgreeOn(t *testing.T) {
	g := crowded()
	for i := range g.Nodes {
		if g.Nodes[i].Type != "pod" {
			continue
		}
		g.Nodes[i].Coverage = &core.Coverage{
			State:    core.CoverageFlowing,
			Evidence: []core.Evidence{{Kind: core.EvidenceObserved}},
		}
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	for _, n := range out.Nodes {
		if !IsFold(n.ID) {
			continue
		}
		if n.Coverage == nil || n.Coverage.State != core.CoverageFlowing {
			t.Fatalf("four boxes whose logs are flowing folded into one nobody knows about: %#v", n.Coverage)
		}
	}
}

// A box that stands for things in two places belongs to neither, and a box
// standing for a queue, a worker and a bucket is none of them.
func TestAStandInCarriesOnlyWhatItsMembersAgreeOn(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	g.Groups = []core.Group{
		{ID: "ns:prod", Axis: core.AxisNetwork, Type: "namespace", Label: "prod"},
		{ID: "ns:stage", Axis: core.AxisNetwork, Type: "namespace", Label: "stage"},
	}
	g.Nodes = []core.Node{
		{ID: "svc:api", Type: "service", Name: "api"},
		{ID: "cm:a", Type: "configmap", Name: "a", Provider: "aws", Groups: map[string]string{core.AxisNetwork: "ns:prod"}},
		{ID: "cm:b", Type: "configmap", Name: "b", Provider: "gcp", Groups: map[string]string{core.AxisNetwork: "ns:stage"}},
	}
	g.Edges = []core.Edge{
		{From: "svc:api", To: "cm:a", Kind: core.EdgeIACRef, Relation: "reads"},
		{From: "svc:api", To: "cm:b", Kind: core.EdgeIACRef, Relation: "reads"},
	}
	g.Normalize()

	// Two config maps in different namespaces and from different providers are
	// not one box, however alike they look from the service they hang off.
	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldLeaves}})
	present := ids(out)
	if !present["cm:a"] || !present["cm:b"] {
		t.Fatalf("attachments in two places and two providers were folded together: %#v", present)
	}
}

// A run of different things is not any of them.
func TestAChainStandInClaimsNoTypeOrPlace(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: core.AxisNetwork}}
	g.Groups = []core.Group{
		{ID: "ns:a", Axis: core.AxisNetwork, Type: "namespace", Label: "a"},
		{ID: "ns:b", Axis: core.AxisNetwork, Type: "namespace", Label: "b"},
	}
	g.Nodes = []core.Node{
		{ID: "in:gateway", Type: "service", Name: "gateway"},
		{ID: "q:one", Type: "queue", Name: "one", Groups: map[string]string{core.AxisNetwork: "ns:a"}},
		{ID: "w:worker", Type: "worker", Name: "worker", Groups: map[string]string{core.AxisNetwork: "ns:b"}},
		{ID: "out:bucket", Type: "bucket", Name: "bucket"},
	}
	chain := []string{"in:gateway", "q:one", "w:worker", "out:bucket"}
	for i := 1; i < len(chain); i++ {
		g.Edges = append(g.Edges, core.Edge{From: chain[i-1], To: chain[i], Kind: core.EdgeIACRef, Relation: "calls"})
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldChain}})
	for _, n := range out.Nodes {
		if !IsFold(n.ID) {
			continue
		}
		if n.Type != FoldChain {
			t.Fatalf("a run of a queue and a worker is drawn as a %q", n.Type)
		}
		if n.Groups[core.AxisNetwork] != "" {
			t.Fatalf("a box standing for things in two namespaces was drawn in %q", n.Groups[core.AxisNetwork])
		}
	}
}

// A name is characters, not bytes. Cutting a multi-byte character in half
// leaves bytes that are not text, and every check downstream lets them through.
func TestACommonNameIsTrimmedByCharacters(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "svc:api", Type: "service", Name: "api"}}
	for _, suffix := range []string{"あ", "い", "う", "え"} {
		id := "pod:" + suffix
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "pod", Name: "注文処理" + suffix})
		g.Edges = append(g.Edges, core.Edge{From: "svc:api", To: id, Kind: core.EdgeIACRef, Relation: "selects"})
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	for _, n := range out.Nodes {
		if !IsFold(n.ID) {
			continue
		}
		for _, r := range n.Name {
			if r == '�' {
				t.Fatalf("the label is not text: %q", n.Name)
			}
		}
		if n.Name != "注文処理 ×4" {
			t.Fatalf("got %q, want the part of the names they agree on", n.Name)
		}
	}
}

// A route that folded is still the same route, so a reading about it follows
// it to its new name rather than being dropped for having moved.
func TestAReadingFollowsARouteThroughAFold(t *testing.T) {
	g := core.New()
	chain := []string{"a", "b", "c", "d"}
	for _, id := range chain {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	for i := 1; i < len(chain); i++ {
		g.Edges = append(g.Edges, core.Edge{From: chain[i-1], To: chain[i], Kind: core.EdgeObserved, Relation: "calls"})
	}
	g.Paths = []core.Path{{Nodes: chain, Kind: core.EdgeObserved}}
	value := 512.0
	g.Observations = []core.Observation{
		{Subject: core.PathKey(chain), Metric: "path_requests", Value: &value},
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldChain}})
	if err := out.Validate(); err != nil {
		t.Fatalf("the folded graph is not valid: %v", err)
	}
	if len(out.Paths) != 1 {
		t.Fatalf("the route did not survive: %#v", out.Paths)
	}
	want := out.Paths[0].Key()
	found := false
	for _, o := range out.Observations {
		if o.Subject == want && o.Value != nil && *o.Value == 512 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the reading was dropped when the route it is about folded: %#v", out.Observations)
	}
}

// A claim is more than who said so. Two things the same person asserted with
// different notes are still two things that person asserted — so they fold —
// but the box must not show one of those notes as though it had been said
// about all of them.
func TestAFoldCarriesOnlyTheClaimItsMembersAllMade(t *testing.T) {
	confidence := 0.8
	g := crowded()
	for i := range g.Nodes {
		switch g.Nodes[i].ID {
		case "pod:worker-a", "pod:worker-b", "pod:worker-c", "pod:worker-d":
			g.Nodes[i].Claim = &core.Claim{Origin: core.OriginHuman, Author: "operator"}
		}
	}
	// Only one of them carries a note and a confidence.
	for i := range g.Nodes {
		if g.Nodes[i].ID == "pod:worker-a" {
			g.Nodes[i].Claim.Note = "found by hand"
			g.Nodes[i].Claim.Confidence = &confidence
		}
	}
	g.Normalize()

	out, _ := folded(t, g, FoldOptions{Rules: []string{FoldTwins}})
	for _, n := range out.Nodes {
		if !IsFold(n.ID) {
			continue
		}
		if n.Claim == nil {
			t.Fatal("four things one person asserted folded into a box nobody claimed")
		}
		if n.Claim.Origin != core.OriginHuman || n.Claim.Author != "operator" {
			t.Fatalf("the part they agreed on was lost: %#v", n.Claim)
		}
		if n.Claim.Note != "" {
			t.Fatalf("one member's note is shown as everybody's: %q", n.Claim.Note)
		}
		if n.Claim.Confidence != nil {
			t.Fatalf("one member's confidence is shown as everybody's: %v", *n.Claim.Confidence)
		}
	}
}

// A fold of things nobody claimed claims nothing, which is what an absent
// claim already means.
func TestAFoldOfParserFindingsClaimsNothing(t *testing.T) {
	out, _ := folded(t, crowded(), FoldOptions{Rules: []string{FoldTwins}})
	for _, n := range out.Nodes {
		if IsFold(n.ID) && n.Claim != nil {
			t.Fatalf("a fold of things a parser found carries %#v", n.Claim)
		}
	}
}
