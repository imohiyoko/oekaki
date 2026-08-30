package loginventory

import (
	"github.com/imohiyoko/oekaki/collectors/loginventory"
	"github.com/imohiyoko/oekaki/core"
	"testing"
	"time"
)

func TestApplyClassifiedMetadata(t *testing.T) {
	at := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	inv := loginventory.Inventory{Version: "1", Status: &loginventory.PollStatus{StartedAt: at, CompletedAt: at.Add(time.Minute), Fetched: 1, Classified: 1}, Records: []loginventory.ClassifiedRecord{{ID: "log-1", Source: "service:a", ObservedAt: time.Unix(0, 0), Labels: []string{"error"}}}}
	g := core.New()
	g.Nodes = []core.Node{{ID: "service:a", Type: "service", Name: "a"}}
	r, err := (Enricher{Inventory: inv}).Enrich(g)
	if err != nil || r.Applied != 1 || len(g.LogRecords) != 1 || len(g.Observations) != 1 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	if g.LogStatus == nil || g.LogStatus.Fetched != 1 || g.LogStatus.CompletedAt == "" {
		t.Fatalf("log status was not joined: %+v", g.LogStatus)
	}
}

func TestApplyResolvesExternalLogSourceByNodeName(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "aws_ecs_service.checkout", Type: "aws_ecs_service", Name: "checkout"}}
	inv := loginventory.Inventory{Version: "1", Records: []loginventory.ClassifiedRecord{{ID: "log-1", Source: "checkout", Labels: []string{"error"}}}}
	r, err := (Enricher{Inventory: inv}).Enrich(g)
	if err != nil || r.Applied != 1 || len(g.LogRecords) != 1 || g.LogRecords[0].Source != "aws_ecs_service.checkout" {
		t.Fatalf("report=%+v records=%+v err=%v", r, g.LogRecords, err)
	}
	n, _ := g.Node("aws_ecs_service.checkout")
	if n.Coverage == nil || n.Coverage.State != core.CoverageFlowing || len(n.Coverage.Evidence) != 1 {
		t.Fatalf("log coverage was not recorded: %+v", n.Coverage)
	}
}

func TestApplyDoesNotDuplicateAnInventoryRecord(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "service:a", Type: "service", Name: "api"}}
	inv := loginventory.Inventory{Version: "1", Records: []loginventory.ClassifiedRecord{{ID: "log-1", Source: "service:a"}}}
	e := Enricher{Inventory: inv}
	if _, err := e.Enrich(g); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Enrich(g); err != nil {
		t.Fatal(err)
	}
	if len(g.LogRecords) != 1 {
		t.Fatalf("record was duplicated: %+v", g.LogRecords)
	}
}

func TestApplyDoesNotGuessAmbiguousExternalLogSource(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "service:a", Type: "service", Name: "api"}, {ID: "service:b", Type: "service", Name: "api"}}
	inv := loginventory.Inventory{Version: "1", Records: []loginventory.ClassifiedRecord{{ID: "log-1", Source: "api"}}}
	r, err := (Enricher{Inventory: inv}).Enrich(g)
	if err != nil || r.Applied != 0 || len(r.Ambiguous) != 1 || len(g.LogRecords) != 0 {
		t.Fatalf("report=%+v records=%+v err=%v", r, g.LogRecords, err)
	}
}
