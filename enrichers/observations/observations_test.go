package observations

import (
	"github.com/imohiyoko/oekaki/core"
	"testing"
)

func TestParseAndApply(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.observations","version":"1","observations":[{"subject":"service:a","metric":"latency","value":820,"unit":"ms","state":"abnormal"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	g := core.New()
	g.Nodes = []core.Node{{ID: "service:a", Type: "service", Name: "a"}}
	r, err := (Enricher{Docs: []*Document{d}}).Enrich(g)
	if err != nil || r.Applied != 1 || len(g.Observations) != 1 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
}

func TestThresholdSetsState(t *testing.T) {
	d, err := Parse([]byte(`{"kind":"oekaki.observations","version":"1","observations":[{"subject":"service:a","metric":"latency","value":820,"threshold":{"operator":">","value":500}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	g := core.New()
	g.Nodes = []core.Node{{ID: "service:a", Type: "service", Name: "a"}}
	if _, err = (Enricher{Docs: []*Document{d}}).Enrich(g); err != nil {
		t.Fatal(err)
	}
	if g.Observations[0].State != "abnormal" {
		t.Fatalf("state=%q", g.Observations[0].State)
	}
}
