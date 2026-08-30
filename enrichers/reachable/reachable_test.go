package reachable

import (
	"encoding/json"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func TestParseNormalizedReachabilityDocument(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.reachability","version":"1","paths":[{"from":"api","to":"db","protocol":"tcp","port":5432,"allowed":true}]}`))
	if err != nil || len(d.Paths) != 1 || !d.Paths[0].Allowed {
		t.Fatalf("document=%+v err=%v", d, err)
	}
	if _, err := json.Marshal(d); err != nil {
		t.Fatal(err)
	}
}

func TestIngressExpandsSecurityGroups(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "app", Type: "aws_instance", Name: "app"}, {ID: "db", Type: "aws_db_instance", Name: "db"}, {ID: "aws_security_group.app", Type: "aws_security_group", Name: "app", Attrs: map[string]any{"ingress": []any{map[string]any{"protocol": "tcp", "from_port": 5432.0, "to_port": 5432.0, "security_groups": []any{"aws_security_group.app"}}}}}, {ID: "aws_security_group.db", Type: "aws_security_group", Name: "db", Attrs: map[string]any{"ingress": []any{map[string]any{"protocol": "tcp", "from_port": 5432.0, "to_port": 5432.0, "security_groups": []any{"aws_security_group.app"}}}}}}
	g.Edges = []core.Edge{{From: "app", To: "aws_security_group.app", Kind: core.EdgeIACRef}, {From: "db", To: "aws_security_group.db", Kind: core.EdgeIACRef}}
	r, err := (Enricher{}).Enrich(g)
	if err != nil || r.Applied != 1 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	if g.Edges[len(g.Edges)-1].Relation != "reachable" {
		t.Fatal("missing reachable relation")
	}
}

func TestStandaloneSecurityGroupRuleExpandsIngressAndPublicEgress(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "app", Type: "aws_instance", Name: "app"},
		{ID: "db", Type: "aws_db_instance", Name: "db"},
		{ID: "aws_security_group.app", Type: "aws_security_group", Name: "app"},
		{ID: "aws_security_group.db", Type: "aws_security_group", Name: "db"},
		{ID: "aws_security_group_rule.db_ingress", Type: "aws_security_group_rule", Name: "db-ingress", Attrs: map[string]any{"protocol": "tcp", "from_port": 5432.0, "to_port": 5432.0}},
		{ID: "aws_security_group_rule.app_egress", Type: "aws_security_group_rule", Name: "app-egress", Attrs: map[string]any{"type": "egress", "protocol": "-1", "cidr_blocks": []any{"0.0.0.0/0"}}},
	}
	g.Edges = []core.Edge{
		{From: "app", To: "aws_security_group.app", Kind: core.EdgeIACRef},
		{From: "db", To: "aws_security_group.db", Kind: core.EdgeIACRef},
		{From: "aws_security_group_rule.db_ingress", To: "aws_security_group.db", Kind: core.EdgeIACRef, Attrs: map[string]any{"attribute": "security_group_id"}},
		{From: "aws_security_group_rule.db_ingress", To: "aws_security_group.app", Kind: core.EdgeIACRef, Attrs: map[string]any{"attribute": "source_security_group_id"}},
		{From: "aws_security_group_rule.app_egress", To: "aws_security_group.app", Kind: core.EdgeIACRef, Attrs: map[string]any{"attribute": "security_group_id"}},
	}
	if _, err := (Enricher{}).Enrich(g); err != nil {
		t.Fatal(err)
	}
	var ingress, egress, public bool
	for _, e := range g.Edges {
		if e.Kind != core.EdgeReachable {
			continue
		}
		if e.From == "aws_security_group_rule.db_ingress" || e.To == "aws_security_group_rule.db_ingress" || e.From == "aws_security_group_rule.app_egress" || e.To == "aws_security_group_rule.app_egress" {
			t.Fatalf("security-group rule was treated as a workload: %#v", e)
		}
		if e.From == "app" && e.To == "db" {
			ingress = true
		}
		if e.From == "app" && e.To == "external:internet" {
			egress = true
		}
		if e.From == "external:internet" && e.To == "db" {
			public = true
		}
	}
	if !ingress || !egress || public {
		t.Fatalf("unexpected reachability: ingress=%t egress=%t public-ingress=%t", ingress, egress, public)
	}
	var observed bool
	for _, o := range g.Observations {
		if o.Subject == "app" && o.Metric == "internet_reachability" && o.State == "abnormal" {
			observed = true
		}
	}
	if !observed {
		t.Fatal("public egress did not produce an internet reachability observation")
	}
}

func TestReachabilityDocumentClaimIsPreservedOnAllowedEdge(t *testing.T) {
	doc := &Document{Paths: []Path{{
		From: "app", To: "db", Allowed: true,
		Claim: &core.Claim{Origin: core.OriginHuman, Author: "network-team", Note: "verified in policy engine"},
	}}}
	g := core.New()
	g.Nodes = []core.Node{{ID: "app", Type: "service", Name: "app"}, {ID: "db", Type: "database", Name: "db"}}
	if _, err := (Enricher{Documents: []*Document{doc}}).Enrich(g); err != nil {
		t.Fatal(err)
	}
	for _, edge := range g.Edges {
		if edge.From == "app" && edge.To == "db" && edge.Kind == core.EdgeReachable {
			if edge.Claim == nil || edge.Claim.Origin != core.OriginHuman || edge.Claim.Author != "network-team" {
				t.Fatalf("document claim was lost: %#v", edge.Claim)
			}
			return
		}
	}
	t.Fatal("allowed reachability edge was not created")
}

func TestReachabilityRelationHasIndependentIdentity(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "app", Type: "service", Name: "app"}, {ID: "db", Type: "database", Name: "db"}}
	exposureClaim := &core.Claim{Origin: core.OriginHuman, Author: "security-team"}
	g.Edges = []core.Edge{{
		From: "app", To: "db", Kind: core.EdgeReachable, Relation: "exposes",
		Attrs: map[string]any{"endpoint": "public.example"}, Claim: exposureClaim,
	}}
	docClaim := &core.Claim{Origin: core.OriginAI, Author: "policy-agent"}
	doc := &Document{Paths: []Path{{From: "app", To: "db", Allowed: true, Protocol: "tcp", Port: 443, Claim: docClaim}}}
	if _, err := (Enricher{Documents: []*Document{doc}}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	relations := map[string]core.Edge{}
	for _, edge := range g.Edges {
		if edge.From == "app" && edge.To == "db" && edge.Kind == core.EdgeReachable {
			relations[edge.Relation] = edge
		}
	}
	if len(relations) != 2 {
		t.Fatalf("reachable relations were merged: %#v", relations)
	}
	if got := relations["exposes"]; got.Claim == nil || got.Claim.Author != "security-team" || got.Attrs["endpoint"] != "public.example" {
		t.Fatalf("exposure edge was mutated: %#v", got)
	}
	if got := relations["reachable"]; got.Claim == nil || got.Claim.Author != "policy-agent" || got.Attrs["protocol"] != "tcp" {
		t.Fatalf("document edge was not independently retained: %#v", got)
	}
}

func TestEmptyReachabilityRelationSuppressesCanonicalDuplicate(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "app", Type: "service", Name: "app"}, {ID: "db", Type: "database", Name: "db"}}
	g.Edges = []core.Edge{{
		From: "app", To: "db", Kind: core.EdgeReachable,
		Attrs: map[string]any{"source": "overlay"},
	}}
	doc := &Document{Paths: []Path{{From: "app", To: "db", Allowed: true, Protocol: "tcp", Port: 443}}}
	if _, err := (Enricher{Documents: []*Document{doc}}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	var edges []core.Edge
	for _, edge := range g.Edges {
		if edge.From == "app" && edge.To == "db" && edge.Kind == core.EdgeReachable {
			edges = append(edges, edge)
		}
	}
	if len(edges) != 1 {
		t.Fatalf("got %d parallel reachable edges, want one: %#v", len(edges), edges)
	}
	if edges[0].Relation != "" || edges[0].Attrs["source"] != "overlay" || edges[0].Attrs["protocol"] != "tcp" {
		t.Fatalf("legacy reachable edge was not merged in place: %#v", edges[0])
	}
}

func TestDuplicateReachabilityDocumentsAreOrderIndependent(t *testing.T) {
	high, low := 0.9, 0.4
	docs := []*Document{
		{Paths: []Path{{From: "app", To: "db", Allowed: true, Protocol: "udp", Port: 80, Reason: "z-source", Claim: &core.Claim{Origin: core.OriginHuman, Author: "network-team", Confidence: &high}}}},
		{Paths: []Path{{From: "app", To: "db", Allowed: true, Protocol: "tcp", Port: 443, Reason: "a-source", Claim: &core.Claim{Origin: core.OriginHuman, Author: "network-team", Confidence: &low}}}},
	}
	orders := [][]*Document{{docs[0], docs[1]}, {docs[1], docs[0]}}
	var first string
	for i, order := range orders {
		g := core.New()
		g.Nodes = []core.Node{{ID: "app", Type: "service", Name: "app"}, {ID: "db", Type: "database", Name: "db"}}
		if _, err := (Enricher{Documents: order}).Enrich(g); err != nil {
			t.Fatal(err)
		}
		var edge *core.Edge
		for j := range g.Edges {
			if g.Edges[j].From == "app" && g.Edges[j].To == "db" && g.Edges[j].Relation == "reachable" {
				edge = &g.Edges[j]
				break
			}
		}
		if edge == nil || edge.Claim == nil || edge.Claim.Confidence == nil || *edge.Claim.Confidence != low {
			t.Fatalf("order %d: canonical claim was not selected: %#v", i, edge)
		}
		if edge.Attrs["protocol"] != "tcp" || edge.Attrs["port"] != 443 || edge.Attrs["reason"] != "a-source" {
			t.Fatalf("order %d: attrs were not merged canonically: %#v", i, edge.Attrs)
		}
		encoded, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(encoded)
		} else if string(encoded) != first {
			t.Fatalf("document order changed graph:\n%s\n---\n%s", first, encoded)
		}
	}
}

func TestNormalizeRefOnlyRemovesIDSuffix(t *testing.T) {
	if got := normalizeRef("aws_security_group.identity.id"); got != "aws_security_group.identity" {
		t.Fatalf("normalizeRef = %q", got)
	}
}
