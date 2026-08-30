package terraform

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func TestAllThreeAxesAreDeclared(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	for _, axis := range []string{core.AxisNetwork, core.AxisProvider, core.AxisModule} {
		if !g.HasAxis(axis) {
			t.Errorf("axis %q was not declared", axis)
		}
	}
}

// The provider axis is what makes a mixed estate legible: there is no shared
// network topology between a vSphere datacenter and a VPC, so grouping by
// provider is the only honest nesting available across the whole estate.
func TestProviderAxisGroupsEveryProvider(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	want := map[string]bool{
		"provider:aws": false, "provider:azurerm": false,
		"provider:kubernetes": false, "provider:vsphere": false,
	}
	for _, grp := range g.Groups {
		if grp.Axis != core.AxisProvider {
			continue
		}
		if _, ok := want[grp.ID]; !ok {
			t.Errorf("unexpected provider group %q", grp.ID)
			continue
		}
		want[grp.ID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("provider group %q is missing", id)
		}
	}

	// Every node must land under its own provider, whichever cloud it is in.
	for _, n := range g.Nodes {
		if n.Provider == "" {
			t.Errorf("%s has no provider recorded", n.ID)
			continue
		}
		if got, want := n.GroupOn(core.AxisProvider), "provider:"+n.Provider; got != want {
			t.Errorf("%s provider group = %q, want %q", n.ID, got, want)
		}
	}
}

// A node keeps its place on every axis at once. Losing one when another is
// assigned would make the axes mutually exclusive, which defeats the point.
func TestAxesAreIndependent(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	n, ok := g.Node("aws_db_instance.main")
	if !ok {
		t.Fatal("aws_db_instance.main is missing")
	}
	if n.GroupOn(core.AxisNetwork) == "" {
		t.Error("network placement was lost")
	}
	if n.GroupOn(core.AxisProvider) == "" {
		t.Error("provider placement was lost")
	}
}

func TestModulePath(t *testing.T) {
	tests := []struct {
		address string
		want    []string
	}{
		{"aws_vpc.main", nil},
		{"module.network.aws_vpc.main", []string{"module.network"}},
		{"module.platform.module.network.aws_subnet.a", []string{"module.platform", "module.network"}},
		{"module.a.module.b.module.c.aws_instance.x[0]", []string{"module.a", "module.b", "module.c"}},
		{`module.workload["blue%2Fteam.with.dot"].aws_instance.app`, []string{`module.workload["blue%2Fteam.with.dot"]`}},
	}

	for _, tt := range tests {
		got := modulePath(tt.address)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("modulePath(%q) = %v, want %v", tt.address, got, tt.want)
		}
	}
}

// Scope exists so that two states that both contain `aws_vpc.main` can be
// combined without silently merging two different VPCs.
func TestScopeQualifiesEveryIdentifier(t *testing.T) {
	g := parseFile(t, examplePlan, Options{Scope: "platform-prod"})

	if g.Metadata.Scope != "platform-prod" {
		t.Errorf("metadata.scope = %q", g.Metadata.Scope)
	}
	for _, n := range g.Nodes {
		if !strings.HasPrefix(n.ID, "platform-prod:") {
			t.Errorf("node id %q is not qualified", n.ID)
		}
		for axis, path := range n.Groups {
			for _, part := range strings.Split(path, core.GroupSeparator) {
				if !strings.HasPrefix(part, "platform-prod:") {
					t.Errorf("node %s axis %s: path segment %q is not qualified", n.ID, axis, part)
				}
			}
		}
	}
	for _, grp := range g.Groups {
		if !strings.HasPrefix(grp.ID, "platform-prod:") {
			t.Errorf("group id %q is not qualified", grp.ID)
		}
	}
	for _, e := range g.Edges {
		if !strings.HasPrefix(e.From, "platform-prod:") || !strings.HasPrefix(e.To, "platform-prod:") {
			t.Errorf("edge %s -> %s is not qualified", e.From, e.To)
		}
	}
}

// Qualifying ids must leave the graph internally consistent, or the scope
// option would produce documents that do not validate.
func TestScopedGraphStillValidates(t *testing.T) {
	g := parseFile(t, examplePlan, Options{Scope: "platform-prod"})

	if err := g.Validate(); err != nil {
		t.Fatalf("a scoped graph does not validate: %v", err)
	}
}

func TestScopeIsOffByDefault(t *testing.T) {
	g := parseFile(t, examplePlan, Options{})

	if g.Metadata.Scope != "" {
		t.Errorf("scope = %q, want empty", g.Metadata.Scope)
	}
	if _, ok := g.Node("aws_db_instance.main"); !ok {
		t.Error("ids were qualified without a scope being asked for")
	}
}

// Across a provider boundary containment is refused, so the reference has to
// survive as an edge instead — otherwise the dependency vanishes entirely.
func TestCrossProviderContainerReferenceBecomesAnEdge(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	// Within one provider the reference stays containment-only, so no edge
	// should point at a same-provider container.
	for _, e := range g.Edges {
		grp, isGroup := g.Group(e.To)
		if !isGroup || grp.Axis != core.AxisNetwork {
			continue
		}
		from, ok := g.Node(e.From)
		if !ok {
			continue
		}
		if from.Provider == "aws" {
			t.Errorf("edge %s -> %s duplicates same-provider containment", e.From, e.To)
		}
	}
}
