package views

import (
	"fmt"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// estateWithCode is a container joined to a repository, and that repository's
// code — the shape a build record leaves behind once somebody has said which
// input the repository is.
func estateWithCode(t *testing.T, placed bool) *core.Graph {
	t.Helper()
	// Every node of a combined graph carries the input it came from, this one
	// included. Which input the repository's *code* is, is a different
	// question with a different answer, and only somebody's mapping says it.
	repo := core.Node{ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
		Attrs: map[string]any{"repository": "repo-1-estate"}}
	if placed {
		repo.Attrs["code_input"] = "repo-2-svc"
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
	a, err := BuildAtlas(estateWithCode(t, true), AtlasOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Diagrams) > 1 {
		t.Fatalf("%d diagrams, over the limit", len(a.Diagrams))
	}
	if pageOf(a, "codemap:repository:acme/checkout") != nil {
		t.Fatal("the code map ignored the limit")
	}
	if openingOf(pageOf(a, "detail:task"), "repository:acme/checkout") != nil {
		t.Error("a door was left pointing at a page that was never built")
	}
}

// The trail does not stop here. A reader who arrived from a running container
// is on the way to something smaller, and a page whose boxes open nothing ends
// the descent at the point it was meant to keep going.
func TestTheCodeMapsBoxesOpenTheWayTheyDoAnywhereElse(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, true), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repository:acme/checkout")
	if page == nil {
		t.Fatal("there is no code map")
	}
	for _, want := range []string{
		"repo-2-svc:file:handler/http.go",
		"repo-2-svc:file:handler/http.go#Handle",
		"repo-2-svc:package:net/http",
	} {
		door := openingOf(page, want)
		if door == nil {
			t.Errorf("%s opens nothing", want)
			continue
		}
		if pageOf(a, door.Diagram) == nil {
			t.Errorf("%s opens %s, which is not a page", want, door.Diagram)
		}
	}
}

// The repository node wears two answers: the input it came from, and the input
// its code is. Reading the first one opened that whole input's code — the
// estate's own graph, in the case that matters, because a repository node
// arrives inside a previous output.
func TestTheCodeMapReadsTheRepositorysOwnAnswerAndNotTheOneEveryNodeCarries(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "task", Type: "aws_ecs_task_definition", Name: "api",
			Attrs: map[string]any{"image": "img:1", "repository": "repo-1-estate"}},
		{ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-estate", "code_input": "repo-2-svc"}},
		{ID: "repo-2-svc:file/a.go", Type: "code_file", Name: "a.go",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repo-2-svc:file/a.go#A", Type: "code_function", Name: "A",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repo-2-svc:package:net/http", Type: "code_package", Name: "net/http",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		// The estate input's own code — the graph this repository node arrived
		// inside. It is what reading the wrong attribute reaches.
		{ID: "repo-1-estate:file/b.go#B", Type: "code_function", Name: "B",
			Attrs: map[string]any{"repository": "repo-1-estate"}},
	}
	g.Edges = []core.Edge{
		{From: "task", To: "repository:acme/checkout", Kind: core.EdgeObserved, Relation: "built_from"},
		{From: "repo-2-svc:file/a.go", To: "repo-2-svc:package:net/http", Kind: core.EdgeIACRef, Relation: "imports"},
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repository:acme/checkout")
	if page == nil {
		t.Fatal("there is no code map")
	}
	for _, n := range page.Graph.Nodes {
		if n.ID == "repo-1-estate:file/b.go#B" {
			t.Error("another input's code was drawn on this repository's map")
		}
	}
	held := map[string]bool{}
	for _, n := range page.Graph.Nodes {
		held[n.ID] = true
	}
	if !held["repo-2-svc:file/a.go#A"] {
		t.Error("the repository's own code is not on its map")
	}
}

// Two repositories in one estate produced two pages with the same name, which
// is a title only until there are two of them.
func TestTheCodeMapIsNamedAfterItsRepository(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, true), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repository:acme/checkout")
	if page == nil {
		t.Fatal("there is no code map")
	}
	if page.Title != "acme/checkout" {
		t.Errorf("the page is called %q rather than naming its repository", page.Title)
	}
	if page.Subtitle == "" {
		t.Error("the page does not say what kind of page it is")
	}
}

// Relations are read folded everywhere else in this file, and a graph that
// writes `Imports` is not a graph with nothing to say.
func TestTheCodeMapReadsARelationHoweverItIsWritten(t *testing.T) {
	g := estateWithCode(t, true)
	for i := range g.Edges {
		switch g.Edges[i].Relation {
		case "imports":
			g.Edges[i].Relation = "Imports"
		case "calls":
			g.Edges[i].Relation = "Calls"
		}
	}
	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repository:acme/checkout")
	if page == nil {
		t.Fatal("there is no code map")
	}
	if len(page.Graph.Edges) == 0 {
		t.Error("the map draws no lines at all")
	}
	held := map[string]bool{}
	for _, n := range page.Graph.Nodes {
		held[n.ID] = true
	}
	if !held["repo-2-svc:file:handler/http.go"] {
		t.Error("the file that imports is not on the map")
	}
}

// The budget is spent in the order pages are built, and the ordinary
// recursion walks nodes by id — where every function of a repository sorts
// ahead of the repository itself. A repository big enough to fill the budget
// with its own function pages left the container that runs it opening onto
// nothing, which is the one descent this page exists to offer.
func TestABigRepositoryDoesNotSpendTheBudgetOnItsOwnFunctions(t *testing.T) {
	g := estateWithCode(t, true)
	for i := 0; i < 500; i++ {
		g.Nodes = append(g.Nodes, core.Node{
			ID:    fmt.Sprintf("repo-2-svc:file:handler/http.go#f%03d", i),
			Type:  "code_function",
			Name:  fmt.Sprintf("f%03d", i),
			Attrs: map[string]any{"repository": "repo-2-svc"},
		})
		g.Edges = append(g.Edges, core.Edge{
			From: "repo-2-svc:file:handler/http.go",
			To:   fmt.Sprintf("repo-2-svc:file:handler/http.go#f%03d", i),
			Kind: core.EdgeIACRef, Relation: "contains",
		})
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pageOf(a, "codemap:repository:acme/checkout") == nil {
		t.Fatal("the code map lost its place to the pages of the code it maps")
	}
	// The door on the page the repository is placed on, which is the shortest
	// way in. Whether the container's own page also survives a budget this
	// tight is the ordinary question about the budget, and not this one.
	if openingOf(pageOf(a, "level:"), "repository:acme/checkout") == nil {
		t.Error("the repository opens onto nothing")
	}
}
