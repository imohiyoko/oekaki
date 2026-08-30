package ai

import (
	"github.com/imohiyoko/oekaki/core"
	"testing"
)

func TestCandidateIsAIClaimed(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.ai-candidates","version":"1","candidates":[{"from":"a","to":"b","relation":"calls","confidence":0.7}]}`))
	if err != nil {
		t.Fatal(err)
	}
	g := core.New()
	g.Nodes = []core.Node{{ID: "a", Name: "a", Type: "function"}, {ID: "b", Name: "b", Type: "function"}}
	r, err := (Enricher{Docs: []*Document{d}}).Enrich(g)
	if err != nil || r.Applied != 1 || g.Edges[0].Claim == nil || g.Edges[0].Claim.Origin != core.OriginAI {
		t.Fatalf("report=%+v err=%v", r, err)
	}
}

func TestModelCanDeclareOpaqueNodesWithoutOverwritingExistingNodes(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.ai-candidates","version":"1","nodes":[{"id":"library:checkout","type":"code_package","name":"checkout-client","description":"client used to call the checkout service"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	g := core.New()
	g.Nodes = []core.Node{{ID: "library:checkout", Name: "existing", Type: "code_package"}}
	if _, err := (Enricher{Docs: []*Document{d}}).Enrich(g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Name != "existing" {
		t.Fatalf("existing node was overwritten: %+v", g.Nodes)
	}

	d, err = Parse([]byte(`{"kind":"oekaki.ai-candidates","version":"1","nodes":[{"id":"library:payments","type":"code_package","name":"payments-client","description":"client used to call payments"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Enricher{Docs: []*Document{d}}).Enrich(g); err != nil {
		t.Fatal(err)
	}
	n, ok := g.Node("library:payments")
	if !ok || n.Description != "client used to call payments" || n.Claim == nil || n.Claim.Origin != core.OriginAI {
		t.Fatalf("AI node was not added with provenance: %+v", n)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	if _, err := Parse([]byte(`{"kind":"oekaki.ai-candidates","version":"1","extra":true}`)); err == nil {
		t.Fatal("unknown AI candidate field was accepted")
	}
}

func TestParsePreservesStructuredContextNeeds(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.ai-candidates","version":"1","needs":[{"kind":"repository","identifier":"payments-client","reason":"import target was not among selected inputs","repository_hint":"payments","references":["checkout/api.py:4"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Needs) != 1 || d.Needs[0].RepositoryHint != "payments" || len(d.Needs[0].References) != 1 {
		t.Fatalf("structured need was lost: %+v", d.Needs)
	}
}
