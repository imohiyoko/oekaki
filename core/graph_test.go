package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/schema"
)

func TestNormalizeKeepsRelationsAndCanonicalizesObservationDetails(t *testing.T) {
	v1, v2 := 1.0, 2.0
	a := &Graph{Version: Version, Axes: []Axis{}, Nodes: []Node{{ID: "a"}, {ID: "b"}}, Edges: []Edge{
		{From: "a", To: "b", Kind: EdgeIACRef, Relation: "calls"},
		{From: "a", To: "b", Kind: EdgeIACRef, Relation: "reads"},
	}, Observations: []Observation{{Subject: "a", Metric: "m", Value: &v1, Labels: map[string]string{"x": "1"}}, {Subject: "a", Metric: "m", Value: &v2, Labels: map[string]string{"x": "2"}}}}
	b := &Graph{Version: Version, Axes: []Axis{}, Nodes: []Node{{ID: "a"}, {ID: "b"}}, Edges: []Edge{
		{From: "a", To: "b", Kind: EdgeIACRef, Relation: "reads"},
		{From: "a", To: "b", Kind: EdgeIACRef, Relation: "calls"},
	}, Observations: []Observation{{Subject: "a", Metric: "m", Value: &v2, Labels: map[string]string{"x": "2"}}, {Subject: "a", Metric: "m", Value: &v1, Labels: map[string]string{"x": "1"}}}}
	a.Normalize()
	b.Normalize()
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	if string(left) != string(right) {
		t.Fatalf("normalization depends on input order:\n%s\n%s", left, right)
	}
	if len(a.Edges) != 2 {
		t.Fatalf("distinct relations were merged: %#v", a.Edges)
	}
}

func TestValidateRejectsNonFiniteObservationValues(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		v := value
		g := &Graph{Version: Version, Axes: []Axis{}, Nodes: []Node{{ID: "a", Type: "service", Name: "a"}}, Observations: []Observation{{Subject: "a", Metric: "m", Value: &v}}}
		if err := g.Validate(); err == nil {
			t.Errorf("non-finite observation value %v was accepted", value)
		}
	}
}

func TestValidateRejectsInvalidObservations(t *testing.T) {
	tests := []struct {
		name    string
		value   Observation
		wantSub string
	}{
		{name: "empty subject", value: Observation{Metric: "latency"}, wantSub: "empty subject"},
		{name: "unknown subject", value: Observation{Subject: "missing", Metric: "latency"}, wantSub: "unknown subject"},
		{name: "empty metric", value: Observation{Subject: "app"}, wantSub: "empty metric"},
		{name: "unknown threshold operator", value: Observation{Subject: "app", Metric: "latency", Threshold: &Threshold{Operator: "approximately", Value: 10}}, wantSub: "unknown threshold operator"},
		{name: "invalid claim", value: Observation{Subject: "app", Metric: "latency", Evidence: &Claim{Origin: "sensor"}}, wantSub: "unknown claim origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Graph{
				Version:      Version,
				Axes:         []Axis{},
				Nodes:        []Node{{ID: "app", Type: "service", Name: "app"}},
				Groups:       []Group{},
				Observations: []Observation{tt.value},
			}
			err := g.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantSub)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// fixture builds a small VPC/subnet graph used by several tests.
func fixture() *Graph {
	return &Graph{
		Version: Version,
		Axes:    []Axis{{ID: AxisNetwork, Label: "Network topology"}},
		Groups: []Group{
			{ID: "vpc", Axis: AxisNetwork, Type: "vpc", Label: "main", Parent: nil},
			{ID: "sub-a", Axis: AxisNetwork, Type: "subnet", Label: "a", Parent: ptr("vpc")},
			{ID: "sub-b", Axis: AxisNetwork, Type: "subnet", Label: "b", Parent: ptr("vpc")},
		},
		Nodes: []Node{
			{ID: "app", Type: "aws_instance", Name: "app", Groups: map[string]string{AxisNetwork: "vpc/sub-a"}},
			{ID: "db", Type: "aws_db_instance", Name: "db", Groups: map[string]string{AxisNetwork: "vpc/sub-b"}},
		},
		Edges: []Edge{
			{From: "app", To: "db", Kind: EdgeIACRef},
		},
	}
}

func TestGroupPathWalksAncestry(t *testing.T) {
	g := fixture()

	got, err := g.GroupPath("sub-a")
	if err != nil {
		t.Fatalf("GroupPath: %v", err)
	}
	if want := "vpc/sub-a"; got != want {
		t.Errorf("GroupPath(sub-a) = %q, want %q", got, want)
	}

	got, err = g.GroupPath("vpc")
	if err != nil {
		t.Fatalf("GroupPath: %v", err)
	}
	if want := "vpc"; got != want {
		t.Errorf("GroupPath(vpc) = %q, want %q", got, want)
	}
}

func TestGroupPathRejectsCycle(t *testing.T) {
	g := fixture()
	g.Groups[0].Parent = ptr("sub-a")

	if _, err := g.GroupPath("vpc"); err == nil {
		t.Fatal("expected a cycle to be reported")
	}
}

// Normalize is what makes generated graphs reviewable as diffs, so its
// guarantees are worth pinning down: canonical order, and no duplicate edges.
func TestNormalizeSortsAndDeduplicates(t *testing.T) {
	g := &Graph{
		Version: Version,
		Nodes:   []Node{{ID: "b", Type: "t", Name: "b"}, {ID: "a", Type: "t", Name: "a"}},
		Edges: []Edge{
			{From: "b", To: "a", Kind: EdgeReachable},
			{From: "a", To: "b", Kind: EdgeIACRef},
			{From: "a", To: "b", Kind: EdgeIACRef},
		},
		Axes:   []Axis{{ID: AxisNetwork}},
		Groups: []Group{{ID: "z", Axis: AxisNetwork, Type: "vpc", Label: "z"}, {ID: "y", Axis: AxisNetwork, Type: "vpc", Label: "y"}},
	}
	g.Normalize()

	if g.Nodes[0].ID != "a" || g.Nodes[1].ID != "b" {
		t.Errorf("nodes not sorted by id: %+v", g.Nodes)
	}
	if g.Groups[0].ID != "y" || g.Groups[1].ID != "z" {
		t.Errorf("groups not sorted by id: %+v", g.Groups)
	}
	if len(g.Edges) != 2 {
		t.Fatalf("duplicate edge survived: %+v", g.Edges)
	}
	if g.Edges[0].Kind != EdgeIACRef || g.Edges[1].Kind != EdgeReachable {
		t.Errorf("edges not sorted by kind first: %+v", g.Edges)
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	g := fixture()
	g.Normalize()
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
		t.Error("normalizing twice changed the output")
	}
}

func TestValidateCatchesDanglingReferences(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Graph)
		wantSub string
	}{
		{
			name:    "edge to a node that is not there",
			mutate:  func(g *Graph) { g.Edges[0].To = "ghost" },
			wantSub: `unknown target "ghost"`,
		},
		{
			name:    "duplicate node id",
			mutate:  func(g *Graph) { g.Nodes = append(g.Nodes, g.Nodes[0]) },
			wantSub: "duplicate id",
		},
		{
			name:    "group parent that does not exist",
			mutate:  func(g *Graph) { g.Groups[1].Parent = ptr("nowhere") },
			wantSub: `unknown parent "nowhere"`,
		},
		{
			name:    "node in a group path that does not exist",
			mutate:  func(g *Graph) { g.Nodes[0].SetGroup(AxisNetwork, "vpc/made-up") },
			wantSub: "does not exist",
		},
		{
			name:    "group id containing the path separator",
			mutate:  func(g *Graph) { g.Groups[0].ID = "a/b" },
			wantSub: "must not contain",
		},
		{
			name:    "unknown edge kind",
			mutate:  func(g *Graph) { g.Edges[0].Kind = "guessed" },
			wantSub: "unknown kind",
		},
		{
			name:    "wrong version",
			mutate:  func(g *Graph) { g.Version = "9.9" },
			wantSub: "version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := fixture()
			tt.mutate(g)

			err := g.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not mention %q", err, tt.wantSub)
			}
		})
	}
}

func TestValidateAcceptsAGoodGraph(t *testing.T) {
	if err := fixture().Validate(); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
}

func TestLowestCommonAncestor(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{"single path is itself", []string{"vpc/sub-a"}, "vpc/sub-a"},
		{"siblings collapse to the parent", []string{"vpc/sub-a", "vpc/sub-b"}, "vpc"},
		{"ancestor wins over descendant", []string{"vpc/sub-a", "vpc"}, "vpc"},
		{"unrelated trees share nothing", []string{"vpc-a/sub", "vpc-b/sub"}, ""},
		{"a top-level member forces top level", []string{"vpc/sub-a", ""}, ""},
		{"nothing in, nothing out", nil, ""},
		{"deep common prefix is kept", []string{"a/b/c", "a/b/d"}, "a/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LowestCommonAncestor(tt.paths); got != tt.want {
				t.Errorf("LowestCommonAncestor(%v) = %q, want %q", tt.paths, got, tt.want)
			}
		})
	}
}

func TestAssignGroupPathsCollapsesMultipleContainers(t *testing.T) {
	g := fixture()
	g.Nodes[0].SetGroup(AxisNetwork, "")

	err := g.AssignGroupPaths(AxisNetwork, map[string][]string{
		"app": {"sub-a", "sub-b"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A resource in two subnets belongs to neither, so it lands in the VPC.
	if got := g.Nodes[0].GroupOn(AxisNetwork); got != "vpc" {
		t.Errorf("group = %q, want %q", got, "vpc")
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	const doc = `{"version":"0.2","axes":[],"nodes":[],"edges":[],"groups":[],"surprise":1}`

	if _, err := Decode(strings.NewReader(doc)); err == nil {
		t.Fatal("expected an unknown field to be rejected")
	}
}

func TestDecodeEnforcesThePublishedSchema(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{
			name: "missing required collection",
			doc:  `{"version":"0.5","axes":[],"nodes":[],"edges":[]}`,
		},
		{
			name: "observation missing metric",
			doc:  `{"version":"0.5","axes":[],"nodes":[{"id":"app","type":"service","name":"app"}],"edges":[],"groups":[],"observations":[{"subject":"app"}]}`,
		},
		{
			name: "unknown threshold operator",
			doc:  `{"version":"0.5","axes":[],"nodes":[{"id":"app","type":"service","name":"app"}],"edges":[],"groups":[],"observations":[{"subject":"app","metric":"latency","threshold":{"operator":"about","value":1}}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(tt.doc)); err == nil || !strings.Contains(err.Error(), "does not match the IR schema") {
				t.Fatalf("Decode() error = %v, want schema validation error", err)
			}
		})
	}
}

func TestConflictEdgeKeysMatchTheSchemaAndAcceptLegacyDocuments(t *testing.T) {
	canonical := EdgeKey("a", "b", EdgeIACRef)
	if canonical != "edge:YQ.Yg.aWFjX3JlZg." {
		t.Fatalf("relation-less EdgeKey = %q", canonical)
	}
	if got := EdgeKey("a", "b", EdgeIACRef, "calls"); got != "edge:YQ.Yg.aWFjX3JlZg.Y2FsbHM" {
		t.Fatalf("relation EdgeKey = %q", got)
	}

	const document = `{
  "version":"0.4",
  "axes":[],
  "nodes":[{"id":"a","type":"service","name":"a"},{"id":"b","type":"service","name":"b"}],
  "edges":[{"from":"a","to":"b","kind":"iac_ref"}],
  "groups":[],
  "conflicts":[{"target":"a|b|iac_ref|","field":"suppressed","claims":[{"value":"false","claim":{"origin":"human"}},{"value":"true","claim":{"origin":"ai"}}]}]
}`
	g, err := Decode(strings.NewReader(document))
	if err != nil {
		t.Fatalf("legacy 0.4 conflict target was rejected: %v", err)
	}
	if g.Version != Version {
		t.Fatalf("legacy version migrated to %q, want %q", g.Version, Version)
	}
	if !g.HasConflictTarget(canonical, ConflictTargetEdge) || g.HasConflictTarget("missing", ConflictTargetEntity) {
		t.Fatalf("HasConflictTarget does not recognize typed keys")
	}
	if got := g.Conflicts[0].Target; got != canonical || g.Conflicts[0].TargetKind != ConflictTargetEdge {
		t.Fatalf("legacy target normalized to %q, want %q", got, canonical)
	}
}

func TestEdgeKeyRoundTripsArbitrarySeparatorsWithoutCollisions(t *testing.T) {
	first := EdgeKey("a|b", "c", EdgeIACRef, "calls|reads")
	second := EdgeKey("a", "b|c", EdgeIACRef, "calls|reads")
	if first == second {
		t.Fatalf("distinct edge identities collided at %q", first)
	}
	from, to, kind, relation, ok := ParseEdgeKey(first)
	if !ok || from != "a|b" || to != "c" || kind != EdgeIACRef || relation != "calls|reads" {
		t.Fatalf("ParseEdgeKey(%q) = %q, %q, %q, %q, %t", first, from, to, kind, relation, ok)
	}
	for _, malformed := range []string{
		"a|b|iac_ref",
		"edge:YQ.Yg.aWFjX3JlZg",
		"edge:YQ==.Yg.aWFjX3JlZg.",
		"edge:YR.Yg.aWFjX3JlZg.",
		"edge:_w.Yg.aWFjX3JlZg.",
		"edge:.Yg.aWFjX3JlZg.",
		"edge:YQ.Yg.bm90X2Ffa2luZA.",
	} {
		if _, _, _, _, ok := ParseEdgeKey(malformed); ok {
			t.Errorf("ParseEdgeKey accepted malformed or non-canonical key %q", malformed)
		}
	}
	g := &Graph{
		Version: Version,
		Axes:    []Axis{},
		Nodes: []Node{
			{ID: "a|b", Type: "service", Name: "a|b"},
			{ID: "c", Type: "service", Name: "c"},
		},
		Edges:     []Edge{{From: "a|b", To: "c", Kind: EdgeIACRef, Relation: "calls|reads"}},
		Groups:    []Group{},
		Conflicts: []Conflict{{TargetKind: ConflictTargetEdge, Target: first, Field: "suppressed", Claims: conflictClaims()}},
	}
	encoded, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(encoded); err != nil {
		t.Fatalf("canonical edge target does not satisfy the schema: %v", err)
	}
}

func TestTypedEntityAndEdgeTargetsDoNotMerge(t *testing.T) {
	edgeTarget := EdgeKey("a", "b", EdgeIACRef)
	g := &Graph{
		Version: Version,
		Axes:    []Axis{},
		Nodes: []Node{
			{ID: "a", Type: "service", Name: "a"},
			{ID: "b", Type: "service", Name: "b"},
			{ID: edgeTarget, Type: "service", Name: "looks like an edge key"},
		},
		Edges:  []Edge{{From: "a", To: "b", Kind: EdgeIACRef}},
		Groups: []Group{},
		Conflicts: []Conflict{
			{TargetKind: ConflictTargetEntity, Target: edgeTarget, Field: "name", Claims: conflictClaims()},
			{TargetKind: ConflictTargetEdge, Target: edgeTarget, Field: "name", Claims: conflictClaims()},
		},
	}
	g.Normalize()
	if len(g.Conflicts) != 2 {
		t.Fatalf("entity and edge conflicts merged: %#v", g.Conflicts)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("typed targets did not validate: %v", err)
	}
}

func TestValidateRejectsInvalidTypedConflictTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   ConflictTargetKind
		target string
		want   string
	}{
		{name: "unknown target kind", kind: "service", target: "a", want: "unknown target kind"},
		{name: "malformed edge key", kind: ConflictTargetEdge, target: "a|b|iac_ref", want: "malformed edge target"},
		{name: "empty edge endpoint", kind: ConflictTargetEdge, target: "edge:.Yg.aWFjX3JlZg.", want: "malformed edge target"},
		{name: "non UTF-8 edge endpoint", kind: ConflictTargetEdge, target: "edge:_w.Yg.aWFjX3JlZg.", want: "malformed edge target"},
		{name: "unknown encoded edge kind", kind: ConflictTargetEdge, target: "edge:YQ.Yg.bm90X2Ffa2luZA.", want: "malformed edge target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &Graph{
				Version:   Version,
				Axes:      []Axis{},
				Nodes:     []Node{{ID: "a", Type: "service", Name: "a"}, {ID: "b", Type: "service", Name: "b"}},
				Edges:     []Edge{{From: "a", To: "b", Kind: EdgeIACRef}},
				Groups:    []Group{},
				Conflicts: []Conflict{{TargetKind: tc.kind, Target: tc.target, Field: "name", Claims: conflictClaims()}},
			}
			err := g.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDecodeMigratesOnlyVersion04AndEncodeWritesOnlyVersion05(t *testing.T) {
	const legacy = `{"version":"0.4","axes":[],"nodes":[],"edges":[],"groups":[]}`
	g, err := Decode(strings.NewReader(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if g.Version != Version {
		t.Fatalf("Decode() version = %q, want %q", g.Version, Version)
	}
	encoded, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"version": "0.5"`) {
		t.Fatalf("migrated encoding did not contain the current version:\n%s", encoded)
	}
	if err := schema.Validate(encoded); err != nil {
		t.Fatalf("migrated encoding does not satisfy the current schema: %v", err)
	}
	g.Version = legacyVersion
	if _, err := g.MarshalIndent(); err == nil || !strings.Contains(err.Error(), "want \"0.5\"") {
		t.Fatalf("Encode legacy version error = %v", err)
	}
}

func TestDecodeValidatesLegacyBytesBeforeMigration(t *testing.T) {
	const claims = `[{"value":"false","claim":{"origin":"human"}},{"value":"true","claim":{"origin":"ai"}}]`
	tests := []struct {
		name string
		doc  string
	}{
		{
			name: "explicit empty relation is not erased by omitempty",
			doc:  `{"version":"0.4","axes":[],"nodes":[{"id":"a","type":"service","name":"a"},{"id":"b","type":"service","name":"b"}],"edges":[{"from":"a","to":"b","kind":"iac_ref","relation":""}],"groups":[]}`,
		},
		{
			name: "future target discriminator is not accepted as legacy",
			doc:  `{"version":"0.4","axes":[],"nodes":[{"id":"a","type":"service","name":"a"}],"edges":[],"groups":[],"conflicts":[{"target_kind":"entity","target":"a","field":"name","claims":` + claims + `}]}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tc.doc))
			if err == nil || !strings.Contains(err.Error(), "IR 0.4 schema") {
				t.Fatalf("Decode() error = %v, want legacy schema rejection", err)
			}
		})
	}
}

func TestLegacyConflictTargetFormsMigrate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		target   string
		relation string
	}{
		{name: "three part", target: "a|b|iac_ref"},
		{name: "trailing empty fourth part", target: "a|b|iac_ref|"},
		{name: "relation fourth part", target: "a|b|iac_ref|calls", relation: "calls"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edge := `{"from":"a","to":"b","kind":"iac_ref"}`
			if tc.relation != "" {
				edge = fmt.Sprintf(`{"from":"a","to":"b","kind":"iac_ref","relation":%q}`, tc.relation)
			}
			document := fmt.Sprintf(`{"version":"0.4","axes":[],"nodes":[{"id":"a","type":"service","name":"a"},{"id":"b","type":"service","name":"b"}],"edges":[%s],"groups":[],"conflicts":[{"target":%q,"field":"suppressed","claims":[{"value":"false","claim":{"origin":"human"}},{"value":"true","claim":{"origin":"ai"}}]}]}`, edge, tc.target)
			g, err := Decode(strings.NewReader(document))
			if err != nil {
				t.Fatal(err)
			}
			if got := g.Conflicts[0]; got.TargetKind != ConflictTargetEdge || got.Target != EdgeKey("a", "b", EdgeIACRef, tc.relation) {
				t.Fatalf("migrated conflict = %#v", got)
			}
		})
	}
}

func TestAmbiguousLegacyConflictTargetsAreRejected(t *testing.T) {
	for _, tc := range []struct {
		name     string
		nodes    string
		edges    string
		target   string
		wantText string
	}{
		{
			name:     "entity and edge",
			nodes:    `[{"id":"a","type":"service","name":"a"},{"id":"b","type":"service","name":"b"},{"id":"a|b|iac_ref","type":"service","name":"same"}]`,
			edges:    `[{"from":"a","to":"b","kind":"iac_ref"}]`,
			target:   "a|b|iac_ref",
			wantText: "ambiguous legacy target names 2",
		},
		{
			name:     "two edges",
			nodes:    `[{"id":"a|b","type":"service","name":"ab"},{"id":"c","type":"service","name":"c"},{"id":"a","type":"service","name":"a"},{"id":"b|c","type":"service","name":"bc"}]`,
			edges:    `[{"from":"a|b","to":"c","kind":"iac_ref"},{"from":"a","to":"b|c","kind":"iac_ref"}]`,
			target:   "a|b|c|iac_ref",
			wantText: "ambiguous legacy target names 2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := fmt.Sprintf(`{"version":"0.4","axes":[],"nodes":%s,"edges":%s,"groups":[],"conflicts":[{"target":%q,"field":"name","claims":[{"value":"one","claim":{"origin":"human"}},{"value":"two","claim":{"origin":"ai"}}]}]}`, tc.nodes, tc.edges, tc.target)
			_, err := Decode(strings.NewReader(document))
			if err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("Decode() error = %v, want %q", err, tc.wantText)
			}
		})
	}
}

func TestMigrationAggregatesEquivalentLegacyConflictTargets(t *testing.T) {
	typed := EdgeKey("a", "b", EdgeIACRef)
	document := `{"version":"0.4","axes":[],"nodes":[{"id":"a","type":"service","name":"a"},{"id":"b","type":"service","name":"b"}],"edges":[{"from":"a","to":"b","kind":"iac_ref"}],"groups":[],"conflicts":[{"target":"a|b|iac_ref|","field":"suppressed","claims":[{"value":"false","claim":{"origin":"human"}},{"value":"true","claim":{"origin":"ai"}}]},{"target":"a|b|iac_ref","field":"suppressed","claims":[{"value":"false","claim":{"origin":"human"}},{"value":"true","claim":{"origin":"parser"}}]}]}`
	g, err := Decode(strings.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Conflicts) != 1 || g.Conflicts[0].TargetKind != ConflictTargetEdge || g.Conflicts[0].Target != typed || len(g.Conflicts[0].Claims) != 3 {
		t.Fatalf("typed and migrated conflict were not aggregated: %#v", g.Conflicts)
	}
}

func conflictClaims() []ClaimedValue {
	return []ClaimedValue{
		{Value: "one", Claim: Claim{Origin: OriginHuman}},
		{Value: "two", Claim: Claim{Origin: OriginAI}},
	}
}

func TestInputSourceVersionRoundTrips(t *testing.T) {
	g := fixture()
	g.Metadata = &Metadata{Inputs: []InputRef{{ID: "payments", Path: "graph.json", Kind: "graph", SourceVersion: "1.8.2"}}}
	out, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(strings.NewReader(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Metadata.Inputs[0].SourceVersion; got != "1.8.2" {
		t.Fatalf("source_version = %q", got)
	}
}

func TestEncodeEmitsEmptyArraysNotNull(t *testing.T) {
	out, err := New().MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"nodes": []`, `"edges": []`, `"groups": []`} {
		if !strings.Contains(string(out), field) {
			t.Errorf("output is missing %s:\n%s", field, out)
		}
	}
}

func TestEncodeRejectsInvalidProgrammaticGraphBeforeWriting(t *testing.T) {
	g := New()
	g.Nodes = []Node{{ID: "broken", Name: "broken"}}
	var out bytes.Buffer
	err := Encode(&out, g)
	if err == nil || !strings.Contains(err.Error(), "empty type") {
		t.Fatalf("Encode() error = %v, want empty type validation error", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Encode wrote %d bytes before rejecting the graph", out.Len())
	}
}

func TestRoundTrip(t *testing.T) {
	g := fixture()
	out, err := g.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}

	back, err := Decode(strings.NewReader(string(out)))
	if err != nil {
		t.Fatalf("decoding our own output failed: %v", err)
	}

	again, err := back.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(again) {
		t.Errorf("round trip changed the bytes:\n%s\n---\n%s", out, again)
	}
}
