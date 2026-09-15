package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// estateWithCode is a container joined to a repository, and that repository's
// code — the shape a build record leaves behind once somebody has said which
// input the repository is.
func estateWithCode(t *testing.T, placed bool) *core.Graph {
	t.Helper()
	repo := core.Node{ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout"}
	if placed {
		repo.Attrs = map[string]any{"repository": "repo-2-svc"}
	}
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "task", Type: "aws_ecs_task_definition", Name: "api", Attrs: map[string]any{"image": "img:1"}},
		repo,
		{ID: "repo-2-svc:file:handler/http.go", Type: "code_file", Name: "handler/http.go",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repo-2-svc:file:handler/http.go#Handle", Type: "code_function", Name: "Handle",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repo-2-svc:file:handler/http.go#total", Type: "code_function", Name: "total",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repo-2-svc:package:net/http", Type: "code_package", Name: "net/http",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
	}
	g.Edges = []core.Edge{
		{From: "task", To: "repository:acme/checkout", Kind: core.EdgeObserved, Relation: "built_from"},
		{From: "repo-2-svc:file:handler/http.go", To: "repo-2-svc:file:handler/http.go#Handle",
			Kind: core.EdgeIACRef, Relation: "contains"},
		{From: "repo-2-svc:file:handler/http.go", To: "repo-2-svc:package:net/http",
			Kind: core.EdgeIACRef, Relation: "imports"},
		{From: "repo-2-svc:file:handler/http.go#Handle", To: "repo-2-svc:file:handler/http.go#total",
			Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	return g
}

func pageOf(a *Atlas, id string) *Diagram {
	for i, d := range a.Diagrams {
		if d.ID == id {
			return &a.Diagrams[i]
		}
	}
	return nil
}

func openingOf(d *Diagram, element string) *Opening {
	if d == nil {
		return nil
	}
	for i, o := range d.Opens {
		if o.Element == element {
			return &d.Opens[i]
		}
	}
	return nil
}

// The reader clicked a container asking what it runs. The answer behind the
// repository is its code, not the list of workloads sharing the image.
func TestARepositoryOpensItsCode(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, true), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}

	door := openingOf(pageOf(a, "detail:task"), "repository:acme/checkout")
	if door == nil {
		t.Fatal("the container's page has no door to the repository")
	}
	if door.Kind != KindCodemap {
		t.Fatalf("the door leads to a %s page", door.Kind)
	}

	page := pageOf(a, door.Diagram)
	if page == nil {
		t.Fatal("the door leads nowhere")
	}
	held := map[string]bool{}
	for _, n := range page.Graph.Nodes {
		held[n.ID] = true
	}
	for _, want := range []string{
		"repo-2-svc:file:handler/http.go#Handle",
		"repo-2-svc:file:handler/http.go#total",
		"repo-2-svc:package:net/http",
	} {
		if !held[want] {
			t.Errorf("%s is not on the code map", want)
		}
	}
	// The container itself is not: this page is the inside of the repository.
	if held["task"] {
		t.Error("the container was drawn on its own code map")
	}

	relations := map[string]bool{}
	for _, e := range page.Graph.Edges {
		relations[e.Relation] = true
	}
	if !relations["calls"] || !relations["imports"] {
		t.Errorf("the map draws %v; it is meant to say what calls what and what it imports", relations)
	}
	// Every line is one the parser recorded. Nothing joins the code to the
	// estate around it, because nothing wrote that down.
	if relations["built_from"] {
		t.Error("a line was drawn between the code and the estate")
	}
}

// A repository nobody placed has no code to open, and the ordinary page is
// still the honest answer.
func TestARepositoryNobodyPlacedKeepsItsOrdinaryPage(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, false), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	door := openingOf(pageOf(a, "detail:task"), "repository:acme/checkout")
	if door == nil {
		t.Fatal("the repository has no door at all")
	}
	if door.Kind == KindCodemap {
		t.Fatal("a repository nobody placed opened a code map")
	}
	if pageOf(a, "codemap:repository:acme/checkout") != nil {
		t.Error("a code map was built for a repository with no input")
	}
}

// A page that does not participate in the bound is not bounded. When the
// budget runs out the map is not built, and the door to it goes with it.
func TestTheCodeMapParticipatesInTheLimit(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, true), AtlasOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Diagrams) > 2 {
		t.Fatalf("%d diagrams, over the limit", len(a.Diagrams))
	}
	if pageOf(a, "codemap:repository:acme/checkout") != nil {
		t.Fatal("the code map ignored the limit")
	}
	if openingOf(pageOf(a, "detail:task"), "repository:acme/checkout") != nil {
		t.Error("a door was left pointing at a page that was never built")
	}
}
