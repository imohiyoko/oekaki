package views

import (
	"fmt"
	"strings"
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
	if pageOf(a, "codemap:repo-2-svc") != nil {
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
	if pageOf(a, "codemap:repo-2-svc") != nil {
		t.Fatal("the code map ignored the limit")
	}
	// The door is on the page the reader is actually on. Asking it of
	// `detail:task` asked nothing: that page was never built either, so the
	// answer was nil whatever prune did.
	if openingOf(pageOf(a, "level:"), "repository:acme/checkout") != nil {
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
	page := pageOf(a, "codemap:repo-2-svc")
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
		{ID: "repo-2-svc:file/a.go#B", Type: "code_function", Name: "B",
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
		{From: "repo-2-svc:file/a.go#A", To: "repo-2-svc:file/a.go#B", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repo-2-svc")
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
	page := pageOf(a, "codemap:repo-2-svc")
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
	page := pageOf(a, "codemap:repo-2-svc")
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
	if pageOf(a, "codemap:repo-2-svc") == nil {
		t.Fatal("the code map lost its place to the pages of the code it maps")
	}
	// The door on the page the repository is placed on, which is the shortest
	// way in. Whether the container's own page also survives a budget this
	// tight is the ordinary question about the budget, and not this one.
	if openingOf(pageOf(a, "level:"), "repository:acme/checkout") == nil {
		t.Error("the repository opens onto nothing")
	}
}

// A second repository on the same page. The budget is spent in the order pages
// are built, so descending into the first repository's own code before the
// second map is made is one repository saved at the cost of the next one.
func TestTwoRepositoriesBothKeepTheirCodeMap(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "task-a", Type: "aws_ecs_task_definition", Name: "a", Attrs: map[string]any{"image": "a:1"}},
		{ID: "task-b", Type: "aws_ecs_task_definition", Name: "b", Attrs: map[string]any{"image": "b:1"}},
		{ID: "repository:acme/aaa", Type: "repository", Name: "acme/aaa",
			Attrs: map[string]any{"code_input": "repo-1-aaa"}},
		{ID: "repository:acme/bbb", Type: "repository", Name: "acme/bbb",
			Attrs: map[string]any{"code_input": "repo-2-bbb"}},
	}
	g.Edges = []core.Edge{
		{From: "task-a", To: "repository:acme/aaa", Kind: core.EdgeObserved, Relation: "built_from"},
		{From: "task-b", To: "repository:acme/bbb", Kind: core.EdgeObserved, Relation: "built_from"},
	}
	for _, scope := range []string{"repo-1-aaa", "repo-2-bbb"} {
		file := scope + ":file:main.go"
		g.Nodes = append(g.Nodes,
			core.Node{ID: file, Type: "code_file", Name: "main.go", Attrs: map[string]any{"repository": scope}},
			core.Node{ID: scope + ":package:net/http", Type: "code_package", Name: "net/http",
				Attrs: map[string]any{"repository": scope}})
		g.Edges = append(g.Edges, core.Edge{From: file, To: scope + ":package:net/http",
			Kind: core.EdgeIACRef, Relation: "imports"})
		for i := 0; i < 600; i++ {
			fn := fmt.Sprintf("%s#f%03d", file, i)
			g.Nodes = append(g.Nodes, core.Node{ID: fn, Type: "code_function",
				Name: fmt.Sprintf("f%03d", i), Attrs: map[string]any{"repository": scope}})
			g.Edges = append(g.Edges, core.Edge{From: file, To: fn, Kind: core.EdgeIACRef, Relation: "contains"})
		}
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"codemap:repo-1-aaa", "codemap:repo-2-bbb"} {
		if pageOf(a, want) == nil {
			t.Errorf("%s was never built", want)
		}
	}
	for _, repo := range []string{"repository:acme/aaa", "repository:acme/bbb"} {
		if openingOf(pageOf(a, "level:"), repo) == nil {
			t.Errorf("%s opens onto nothing", repo)
		}
	}
}

// A child level descends as far as it can before returning, so a big enough
// one spent the budget before this level's own code maps were reached.
func TestABigChildLevelDoesNotCostThisLevelItsCodeMap(t *testing.T) {
	g := estateWithCode(t, true)
	g.Axes = []core.Axis{{ID: "network", Label: "network"}}
	g.Groups = []core.Group{{ID: "prod", Axis: "network", Label: "prod"}}
	for i := 0; i < 600; i++ {
		g.Nodes = append(g.Nodes, core.Node{
			ID: fmt.Sprintf("prod/box-%03d", i), Type: "aws_instance",
			Name:   fmt.Sprintf("box-%03d", i),
			Groups: map[string]string{"network": "prod"},
		})
		g.Edges = append(g.Edges, core.Edge{
			From: fmt.Sprintf("prod/box-%03d", i), To: "task",
			Kind: core.EdgeIACRef, Relation: "references",
		})
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{Axis: "network"})
	if err != nil {
		t.Fatal(err)
	}
	if pageOf(a, "codemap:repo-2-svc") == nil {
		t.Fatal("a child level spent the budget before this level's code map was made")
	}
}

// A box on this page is on a line. A function that calls nothing and is called
// by nothing has nothing to say about flow, and a thousand of them behind a
// container's box is the unreadable single picture the atlas exists to answer
// rather than to reproduce.
func TestTheCodeMapDrawsOnlyWhatIsOnALine(t *testing.T) {
	g := estateWithCode(t, true)
	for i := 0; i < 1200; i++ {
		g.Nodes = append(g.Nodes, core.Node{
			ID:    fmt.Sprintf("repo-2-svc:file:handler/http.go#alone%04d", i),
			Type:  "code_function",
			Name:  fmt.Sprintf("alone%04d", i),
			Attrs: map[string]any{"repository": "repo-2-svc"},
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
	page := pageOf(a, "codemap:repo-2-svc")
	if page == nil {
		t.Fatal("there is no code map")
	}
	for _, n := range page.Graph.Nodes {
		if strings.HasPrefix(n.Name, "alone") {
			t.Fatalf("%s is on the map, and on no line", n.ID)
		}
	}
	// The one that is called is still here, and so is the one that calls it.
	held := map[string]bool{}
	for _, n := range page.Graph.Nodes {
		held[n.ID] = true
	}
	for _, want := range []string{
		"repo-2-svc:file:handler/http.go#Handle",
		"repo-2-svc:file:handler/http.go#total",
	} {
		if !held[want] {
			t.Errorf("%s calls or is called, and is not on the map", want)
		}
	}
}

// A suppressed edge is one somebody said is not there. Drawing a page out of
// denied lines answers "what does this container run" with the thing a person
// went to the trouble of denying.
func TestTheCodeMapDoesNotDrawWhatSomebodyDenied(t *testing.T) {
	g := estateWithCode(t, true)
	for i := range g.Edges {
		switch g.Edges[i].Relation {
		case "calls", "imports":
			g.Edges[i].Suppressed = true
			g.Edges[i].Claim = &core.Claim{Origin: core.OriginHuman, Note: "not real"}
		}
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	if code := CodeOf(g, "repo-2-svc"); len(code) != 0 {
		t.Errorf("denied lines still count as code to draw: %v", code)
	}
	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page := pageOf(a, "codemap:repo-2-svc"); page != nil {
		t.Errorf("a code map was built out of %d denied lines", len(page.Graph.Edges))
	}
}

// A call out of this repository to something that is not on the page put the
// function on it and then dropped the only line it had — a box on a page whose
// whole rule is that there are none.
func TestACallLeavingTheRepositoryDoesNotPutABoxOnTheMap(t *testing.T) {
	g := estateWithCode(t, true)
	g.Nodes = append(g.Nodes, core.Node{
		ID: "repo-2-svc:file:handler/http.go#Alone", Type: "code_function", Name: "Alone",
		Attrs: map[string]any{"repository": "repo-2-svc"},
	})
	// Said by somebody about something this page does not hold.
	g.Edges = append(g.Edges, core.Edge{
		From: "repo-2-svc:file:handler/http.go#Alone", To: "task",
		Kind: core.EdgeIACRef, Relation: "calls",
	})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repo-2-svc")
	if page == nil {
		t.Fatal("there is no code map")
	}
	drawn := map[string]bool{}
	for _, e := range page.Graph.Edges {
		drawn[e.From], drawn[e.To] = true, true
	}
	for _, n := range page.Graph.Nodes {
		if !drawn[n.ID] {
			t.Errorf("%s is on the map, and on no line", n.ID)
		}
	}
}

// A denial is a thing somebody said, and every other page draws it and lets
// --hide-suppressed decide. Dropping it here left no way to learn that the
// call was denied. No box is here because of one — CodeOf leaves them out of
// the choosing — so a denied line only ever joins two boxes already on the
// page.
func TestTheCodeMapDrawsADeniedLineBetweenBoxesThatAreAlreadyThere(t *testing.T) {
	g := estateWithCode(t, true)
	g.Edges = append(g.Edges, core.Edge{
		From: "repo-2-svc:file:handler/http.go#total",
		To:   "repo-2-svc:file:handler/http.go#Handle",
		Kind: core.EdgeIACRef, Relation: "calls",
		Suppressed: true,
		Claim:      &core.Claim{Origin: core.OriginHuman, Note: "not real"},
	})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repo-2-svc")
	if page == nil {
		t.Fatal("there is no code map")
	}
	denied := false
	for _, e := range page.Graph.Edges {
		if e.Suppressed {
			denied = true
		}
	}
	if !denied {
		t.Error("the denied line is not on the page, so nothing can say it was denied")
	}
}

// Denying the build record that joined a workload to this repository is what
// suppressing one is for. It leaves the repository with no neighbour, and the
// guard about neighbours used to take the code map with it — while the command
// line, which asks a different question, went on saying there was a map here.
func TestDenyingTheJoinDoesNotTakeTheCodeMapWithIt(t *testing.T) {
	g := estateWithCode(t, true)
	for i := range g.Edges {
		if g.Edges[i].Relation == "built_from" {
			g.Edges[i].Suppressed = true
			g.Edges[i].Claim = &core.Claim{Origin: core.OriginHuman, Note: "wrong repository"}
		}
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pageOf(a, "codemap:repo-2-svc") == nil {
		t.Fatal("the code map went with the denied join")
	}
	if openingOf(pageOf(a, "level:"), "repository:acme/checkout") == nil {
		t.Error("the repository opens onto nothing")
	}
}

// A line joins two kinds of box: a file to a package, a function to a
// function. An end that would not be chosen takes the line with it, so an end
// chosen for that line would sit on the page with nothing attached — which is
// the one thing this page's rule forbids. Not something this project's own
// readers emit; a graph is a document, and somebody else may write one.
func TestAnEndOfTheWrongKindPutsNoBoxOnTheMap(t *testing.T) {
	g := estateWithCode(t, true)
	g.Nodes = append(g.Nodes, core.Node{
		ID: "repo-2-svc:file:odd.go", Type: "code_file", Name: "odd.go",
		Attrs: map[string]any{"repository": "repo-2-svc"},
	})
	g.Nodes = append(g.Nodes, core.Node{
		ID: "repo-2-svc:file:handler/http.go#Lonely", Type: "code_function", Name: "Lonely",
		Attrs: map[string]any{"repository": "repo-2-svc"},
	})
	// A file importing a function rather than a package, and that function on
	// no other line.
	g.Edges = append(g.Edges, core.Edge{
		From: "repo-2-svc:file:odd.go", To: "repo-2-svc:file:handler/http.go#Lonely",
		Kind: core.EdgeIACRef, Relation: "imports",
	})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repo-2-svc")
	if page == nil {
		t.Fatal("there is no code map")
	}
	drawn := map[string]bool{}
	for _, e := range page.Graph.Edges {
		drawn[e.From], drawn[e.To] = true, true
	}
	for _, n := range page.Graph.Nodes {
		if !drawn[n.ID] {
			t.Errorf("%s is on the map, and on no line", n.ID)
		}
	}
}

// Code carries no group on an estate's axis, and a node with no group on the
// axis is drawn at the root of it — so the front page of the estate was the
// repository's every file, function and type, laid out beside the two things
// the estate is made of. The complaint the atlas answers, reproduced by the
// atlas, and paid for out of the same budget the rest of the estate needed.
func TestTheEstateIsNotTheRepositorysCode(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, true), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := pageOf(a, "level:")
	if root == nil {
		t.Fatal("there is no root level")
	}
	var drawn []string
	for _, n := range root.Graph.Nodes {
		drawn = append(drawn, n.ID)
		if strings.HasPrefix(n.ID, "repo-2-svc:") {
			t.Errorf("%s is drawn on the estate's front page", n.ID)
		}
	}
	if len(drawn) != 2 {
		t.Errorf("the estate is %v", drawn)
	}

	// And still reachable, through the box that opens it.
	somewhere := map[string]bool{}
	for _, d := range a.Diagrams {
		for _, n := range d.Graph.Nodes {
			somewhere[n.ID] = true
		}
	}
	for _, id := range []string{
		"repo-2-svc:file:handler/http.go",
		"repo-2-svc:file:handler/http.go#Handle",
		"repo-2-svc:file:handler/http.go#total",
		"repo-2-svc:package:net/http",
	} {
		if !somewhere[id] {
			t.Errorf("%s is drawn nowhere at all", id)
		}
	}
}

// Nobody said which input this repository's code is, so there is no page for
// it to be drawn on instead. Taking it off the level as well would be taking
// it out of the atlas.
func TestCodeWithNoMapStaysWhereItWas(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, false), AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := pageOf(a, "level:")
	if root == nil {
		t.Fatal("there is no root level")
	}
	found := false
	for _, n := range root.Graph.Nodes {
		if n.ID == "repo-2-svc:file:handler/http.go" {
			found = true
		}
	}
	if !found {
		t.Error("the code is on no level and behind no box")
	}
}

// The lines of a level page are not the code map's business. A node the axis
// put inside a container is drawn here as that container, and dropping it from
// the representatives dropped every line the container had — so an atlas on
// the source axis lost the lines between its top-level directories as soon as
// any box opened a code map.
func TestOpeningACodeMapDoesNotTakeTheLevelsLinesWithIt(t *testing.T) {
	g := estateWithCode(t, true)
	g.Axes = []core.Axis{{ID: "source", Label: "Source"}}
	g.Groups = []core.Group{
		{ID: "dir:handler", Type: "directory", Label: "handler", Axis: "source"},
		{ID: "dir:store", Type: "directory", Label: "store", Axis: "source"},
	}
	g.Nodes = append(g.Nodes,
		core.Node{ID: "repo-2-svc:file:store/db.go", Type: "code_file", Name: "store/db.go",
			Attrs: map[string]any{"repository": "repo-2-svc"}, Groups: map[string]string{"source": "dir:store"}},
		core.Node{ID: "repo-2-svc:package:database/sql", Type: "code_package", Name: "database/sql",
			Attrs: map[string]any{"repository": "repo-2-svc"}, Groups: map[string]string{"source": "dir:store"}})
	for i := range g.Nodes {
		if strings.HasPrefix(g.Nodes[i].ID, "repo-2-svc:file:handler/") {
			g.Nodes[i].Groups = map[string]string{"source": "dir:handler"}
		}
	}
	g.Edges = append(g.Edges,
		core.Edge{From: "repo-2-svc:file:store/db.go", To: "repo-2-svc:package:database/sql",
			Kind: core.EdgeIACRef, Relation: "imports"},
		core.Edge{From: "repo-2-svc:file:handler/http.go", To: "repo-2-svc:file:store/db.go",
			Kind: core.EdgeIACRef, Relation: "calls"})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{Axis: "source"})
	if err != nil {
		t.Fatal(err)
	}
	root := pageOf(a, "level:")
	if root == nil {
		t.Fatal("there is no root level")
	}
	found := false
	for _, e := range root.Graph.Edges {
		if e.From == "dir:handler" && e.To == "dir:store" {
			found = true
		}
	}
	if !found {
		t.Errorf("the line between the directories is gone: %+v", root.Graph.Edges)
	}
}

// A map draws the boxes on a line and no others, so a file that imports
// nothing is on no map. Taking it off the level as well — and what it declares
// with it — left it in the atlas's nowhere: on no page at all.
func TestAFileOnNoLineIsStillDrawnSomewhere(t *testing.T) {
	g := estateWithCode(t, true)
	g.Nodes = append(g.Nodes,
		core.Node{ID: "repo-2-svc:file:model/order.go", Type: "code_file", Name: "model/order.go",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		core.Node{ID: "repo-2-svc:file:model/order.go#Order", Type: "code_type", Name: "Order",
			Attrs: map[string]any{"repository": "repo-2-svc"}})
	g.Edges = append(g.Edges, core.Edge{
		From: "repo-2-svc:file:model/order.go", To: "repo-2-svc:file:model/order.go#Order",
		Kind: core.EdgeIACRef, Relation: "declares"})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	somewhere := map[string]bool{}
	for _, d := range a.Diagrams {
		for _, n := range d.Graph.Nodes {
			somewhere[n.ID] = true
		}
	}
	for _, id := range []string{
		"repo-2-svc:file:model/order.go",
		"repo-2-svc:file:model/order.go#Order",
	} {
		if !somewhere[id] {
			t.Errorf("%s is on no page at all", id)
		}
	}
}

// One repository, here as a box per input, all of them open onto the one
// input's code. A page per box drew that code once per box, under the same
// title, out of the same budget. A page is named after the code it draws, so
// there is one room and a door into it from each box.
func TestTwoBoxesForOneRepositoryAreTwoDoorsIntoOneRoom(t *testing.T) {
	g := estateWithCode(t, true)
	g.Nodes = append(g.Nodes,
		core.Node{ID: "task-b", Type: "aws_ecs_task_definition", Name: "b",
			Attrs: map[string]any{"image": "img:1"}},
		core.Node{ID: "repo-9-fresh:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-9-fresh", "code_input": "repo-2-svc"}})
	g.Edges = append(g.Edges, core.Edge{
		From: "task-b", To: "repo-9-fresh:repository:acme/checkout",
		Kind: core.EdgeObserved, Relation: "built_from"})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	maps := 0
	for _, d := range a.Diagrams {
		if d.Kind == KindCodemap {
			maps++
		}
	}
	if maps != 1 {
		t.Errorf("%d code maps for one input's code", maps)
	}
	for _, box := range []string{"repository:acme/checkout", "repo-9-fresh:repository:acme/checkout"} {
		open := openingOf(pageOf(a, "level:"), box)
		if open == nil {
			t.Errorf("%s opens onto nothing", box)
			continue
		}
		if open.Diagram != "codemap:repo-2-svc" {
			t.Errorf("%s opens %s", box, open.Diagram)
		}
	}
}

// An atlas drawn on the source axis is the code's own structure. A package is
// at the root of that axis because that is where it is — nothing imports a
// directory — so stripping it there took the package boxes, and the import
// lines lifted to them, off the top page of an atlas whose whole subject is
// the code.
func TestTheSourceAxisKeepsWhatItPlacesAtItsRoot(t *testing.T) {
	g := estateWithCode(t, true)
	g.Axes = []core.Axis{{ID: "source", Label: "Source"}}
	g.Groups = []core.Group{{ID: "dir:handler", Type: "directory", Label: "handler", Axis: "source"}}
	for i := range g.Nodes {
		if strings.HasPrefix(g.Nodes[i].ID, "repo-2-svc:file:handler/") {
			g.Nodes[i].Groups = map[string]string{"source": "dir:handler"}
		}
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{Axis: "source"})
	if err != nil {
		t.Fatal(err)
	}
	root := pageOf(a, "level:")
	if root == nil {
		t.Fatal("there is no root level")
	}
	found := false
	for _, n := range root.Graph.Nodes {
		if n.ID == "repo-2-svc:package:net/http" {
			found = true
		}
	}
	if !found {
		t.Error("the package this axis puts at its root is not drawn there")
	}
	line := false
	for _, e := range root.Graph.Edges {
		if e.From == "dir:handler" && e.To == "repo-2-svc:package:net/http" {
			line = true
		}
	}
	if !line {
		t.Errorf("the import lifted to the directory is gone: %+v", root.Graph.Edges)
	}
}

// The code map is made after the level it hangs off, out of the same budget.
// A bound reached first left the code stripped off the level and drawn
// nowhere: prune can take away an opening that leads nowhere, and it cannot
// put a box back.
func TestCodeIsNotStrippedForAMapThereIsNoRoomFor(t *testing.T) {
	a, err := BuildAtlas(estateWithCode(t, true), AtlasOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	somewhere := map[string]bool{}
	for _, d := range a.Diagrams {
		for _, n := range d.Graph.Nodes {
			somewhere[n.ID] = true
		}
	}
	for _, id := range []string{"repo-2-svc:file:handler/http.go", "repo-2-svc:package:net/http"} {
		if !somewhere[id] {
			t.Errorf("%s is on no page at all", id)
		}
	}
}

// A repository whose files all sit at its root has no directories, so no node
// of it carries a group on the source axis. Asking whether some node carried
// one answered "this axis places nothing here" and stripped the code off the
// top page of an atlas whose whole subject is that code.
func TestAFlatRepositoryKeepsItsCodeOnTheSourceAxis(t *testing.T) {
	g := estateWithCode(t, true)
	g.Axes = []core.Axis{{ID: "source", Label: "Source"}}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{Axis: "source"})
	if err != nil {
		t.Fatal(err)
	}
	root := pageOf(a, "level:")
	if root == nil {
		t.Fatal("there is no root level")
	}
	on := map[string]bool{}
	for _, n := range root.Graph.Nodes {
		on[n.ID] = true
	}
	for _, id := range []string{"repo-2-svc:file:handler/http.go", "repo-2-svc:package:net/http"} {
		if !on[id] {
			t.Errorf("%s is not on the source axis's own front page", id)
		}
	}
}

// Two repositories, one with directories and one without, drawn on the axis
// they are the structure of. Deciding per repository meant one of them was
// stripped and the other not, while both were still given a map — so the maps
// the budget was counting were not the maps it was about to make, and the
// stripped repository's code went nowhere.
func TestTheSourceAxisKeepsBothRepositoriesOnATightBudget(t *testing.T) {
	g := core.New()
	g.Axes = []core.Axis{{ID: "source", Label: "Source"}}
	g.Groups = []core.Group{{ID: "dir:handler", Type: "directory", Label: "handler", Axis: "source"}}
	for _, scope := range []string{"repo-2-a", "repo-3-b"} {
		flat := scope == "repo-3-b"
		file := scope + ":file:main.go"
		box := core.Node{ID: scope + ":repository:acme/" + scope, Type: "repository", Name: "acme/" + scope,
			Attrs: map[string]any{"code_input": scope}}
		fileNode := core.Node{ID: file, Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": scope}}
		if !flat {
			fileNode.Groups = map[string]string{"source": "dir:handler"}
		}
		g.Nodes = append(g.Nodes, box, fileNode,
			core.Node{ID: scope + ":package:fmt", Type: "code_package", Name: "fmt",
				Attrs: map[string]any{"repository": scope}})
		g.Edges = append(g.Edges, core.Edge{From: file, To: scope + ":package:fmt",
			Kind: core.EdgeIACRef, Relation: "imports"})
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{Axis: "source", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	somewhere := map[string]bool{}
	for _, d := range a.Diagrams {
		for _, n := range d.Graph.Nodes {
			somewhere[n.ID] = true
		}
	}
	for _, id := range []string{"repo-3-b:file:main.go", "repo-3-b:package:fmt"} {
		if !somewhere[id] {
			t.Errorf("%s is on no page at all", id)
		}
	}
}

// The code map is made after the level whose code it took, out of the same
// budget. Counting what was left at the time was a guess about a moment that
// had not arrived — every other page made in between spends first — so the
// place is set aside before the code is taken, and what is set aside cannot be
// spent by anything else.
func TestEveryStrippedNodeHasAPageToBeOn(t *testing.T) {
	g := estateWithCode(t, true)
	// The box is down a level, so the maps of this level are made before the
	// level it is on exists — and every level before that one spends first.
	g.Axes = []core.Axis{{ID: "containment", Label: "Cluster"}}
	g.Groups = append(g.Groups, core.Group{ID: "ns:z", Type: "namespace", Label: "z", Axis: "containment"})
	for i := range g.Nodes {
		if g.Nodes[i].ID == "repository:acme/checkout" || g.Nodes[i].ID == "task" {
			g.Nodes[i].Groups = map[string]string{"containment": "ns:z"}
		}
	}
	for _, ns := range []string{"a", "b", "c"} {
		g.Groups = append(g.Groups, core.Group{ID: "ns:" + ns, Type: "namespace", Label: ns, Axis: "containment"})
		for i := 0; i < 4; i++ {
			g.Nodes = append(g.Nodes, core.Node{
				ID: fmt.Sprintf("svc:%s/%d", ns, i), Type: "kubernetes_deployment",
				Name: fmt.Sprintf("%s-%d", ns, i), Groups: map[string]string{"containment": "ns:" + ns},
			})
		}
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	for _, limit := range []int{1, 2, 3, 5, 8, 40} {
		a, err := BuildAtlas(g, AtlasOptions{Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		somewhere := map[string]bool{}
		for _, d := range a.Diagrams {
			for _, n := range d.Graph.Nodes {
				somewhere[n.ID] = true
			}
		}
		for _, id := range []string{
			"repo-2-svc:file:handler/http.go",
			"repo-2-svc:file:handler/http.go#Handle",
			"repo-2-svc:file:handler/http.go#total",
			"repo-2-svc:package:net/http",
		} {
			if !somewhere[id] {
				t.Errorf("limit %d: %s is on no page at all", limit, id)
			}
		}
		if len(a.Diagrams) > limit {
			t.Errorf("limit %d: %d diagrams", limit, len(a.Diagrams))
		}
	}
}

// Evidence is carried onto a page for the boxes that page draws. Taking a box
// off it afterwards left an observation about a box that is no longer there —
// a dangling reference, which core.Validate rejects the whole document for, so
// one observation on one function stopped the entire atlas from being written.
func TestLiftingCodeOffTheLevelDoesNotStrandTheEvidence(t *testing.T) {
	g := estateWithCode(t, true)
	g.Observations = []core.Observation{{
		Subject: "repo-2-svc:file:handler/http.go#Handle",
		Metric:  "requests", ObservedAt: "2026-01-01T00:00:00Z",
	}}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatalf("the atlas was not built: %v", err)
	}
	for _, d := range a.Diagrams {
		if err := d.Graph.Validate(); err != nil {
			t.Errorf("%s: %v", d.ID, err)
		}
	}
}

// A box is taken off the front page because a code map draws it. Its own
// detail page is not that: the door to a detail page was on the front page,
// and taking the box away takes the door with it, which leaves a page nobody
// can open. Drawn somewhere is not the test — drawn somewhere a reader can get
// to without this door is.
func TestABoxIsOnlyLiftedWhenAMapDrawsIt(t *testing.T) {
	g := estateWithCode(t, true)
	// On no line, so on no map, and holding something so it has a page.
	g.Nodes = append(g.Nodes,
		core.Node{ID: "repo-2-svc:file:model/order.go", Type: "code_file", Name: "model/order.go",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		core.Node{ID: "repo-2-svc:file:model/order.go#Order", Type: "code_type", Name: "Order",
			Attrs: map[string]any{"repository": "repo-2-svc"}})
	g.Edges = append(g.Edges, core.Edge{
		From: "repo-2-svc:file:model/order.go", To: "repo-2-svc:file:model/order.go#Order",
		Kind: core.EdgeIACRef, Relation: "declares"})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := pageOf(a, "level:")
	if root == nil {
		t.Fatal("there is no root level")
	}
	on := false
	for _, n := range root.Graph.Nodes {
		if n.ID == "repo-2-svc:file:model/order.go" {
			on = true
		}
	}
	if !on {
		t.Error("the file no map draws was taken off the only page that opens it")
	}

	// Every page of the atlas is opened by some other page.
	doors := map[string]bool{}
	for _, d := range a.Diagrams {
		for _, open := range d.Opens {
			doors[open.Diagram] = true
		}
	}
	for _, d := range a.Diagrams {
		if d.ID != a.Root && !doors[d.ID] {
			t.Errorf("%s is a page nothing opens", d.ID)
		}
	}
}

// A monorepo: two repositories built out of one input, so both boxes open the
// one code map. Naming the page after whichever box reached it first meant
// clicking the other one arrived at a page named after its neighbour, and the
// trail back up led to a level that box is not on.
func TestOneRoomWithTwoNamesIsNamedAfterNeither(t *testing.T) {
	g := estateWithCode(t, true)
	g.Axes = []core.Axis{{ID: "containment", Label: "Cluster"}}
	g.Groups = []core.Group{{ID: "ns:other", Type: "namespace", Label: "other", Axis: "containment"}}
	g.Nodes = append(g.Nodes,
		core.Node{ID: "task-b", Type: "aws_ecs_task_definition", Name: "b",
			Attrs: map[string]any{"image": "img:2"}, Groups: map[string]string{"containment": "ns:other"}},
		core.Node{ID: "repository:acme/worker", Type: "repository", Name: "acme/worker",
			Attrs:  map[string]any{"code_input": "repo-2-svc"},
			Groups: map[string]string{"containment": "ns:other"}})
	g.Edges = append(g.Edges, core.Edge{From: "task-b", To: "repository:acme/worker",
		Kind: core.EdgeObserved, Relation: "built_from"})
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}

	a, err := BuildAtlas(g, AtlasOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page := pageOf(a, "codemap:repo-2-svc")
	if page == nil {
		t.Fatal("there is no code map")
	}
	if page.Title == "acme/checkout" || page.Title == "acme/worker" {
		t.Errorf("the shared page is named after one of them: %q", page.Title)
	}
	if page.Parent != "level:" {
		t.Errorf("the trail back up goes to %q, which only one of them is on", page.Parent)
	}
}
