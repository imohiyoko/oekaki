package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

const buildRecord = `{
  "kind": "oekaki.builds",
  "version": "0.1",
  "builds": [{
    "repository": "acme/checkout",
    "commit": "9f1c0f2e",
    "run": { "id": "17243", "workflow": "release", "url": "https://ci.example/17243" },
    "images": [{ "reference": "registry.example/checkout:1.4.0" }]
  }]
}`

func buildsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "builds.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// An estate with something running an image, which is the only thing a build
// record can join to.
func runningEstate(t *testing.T) string {
	t.Helper()
	g := core.New()
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0"},
	}}
	g.Normalize()
	return graphFile(t, g)
}

func TestABuildRecordJoinsWhatIsRunningToWhatBuiltIt(t *testing.T) {
	r := mustRun(t, "", "graph", runningEstate(t), "--builds", buildsFile(t, buildRecord))

	g := graphOf(t, r.stdout)
	if _, ok := g.Node("repository:acme/checkout"); !ok {
		t.Fatalf("the repository is not here: %#v", g.Nodes)
	}
	found := false
	for _, e := range g.Edges {
		if e.From == "workload:shop/checkout" && e.To == "repository:acme/checkout" && e.Relation == "built_from" {
			found = true
			if e.Claim == nil || !strings.Contains(e.Claim.Note, "17243") {
				t.Errorf("the edge does not name the run: %+v", e.Claim)
			}
		}
	}
	if !found {
		t.Errorf("no built_from edge: %#v", g.Edges)
	}
	if !strings.Contains(r.stderr, "builds:") {
		t.Errorf("nothing was reported:\n%s", r.stderr)
	}
}

// Whether a CI system belongs in the picture is the estate's decision, and the
// command is often assembled by a wrapper that passes --builds regardless.
func TestNoBuildsRefusesTheRecord(t *testing.T) {
	r := mustRun(t, "", "graph", runningEstate(t),
		"--builds", buildsFile(t, buildRecord), "--no-builds")

	g := graphOf(t, r.stdout)
	if _, ok := g.Node("repository:acme/checkout"); ok {
		t.Error("the record was read anyway")
	}
	if !strings.Contains(r.stderr, "--no-builds") {
		t.Errorf("nothing said the record was refused:\n%s", r.stderr)
	}
}

func TestBuildRepoPointsTheEdgeAtWhatSomebodyNamed(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0"}},
		{ID: "file:main.go", Type: "code_file", Name: "main.go"},
	}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=file:main.go")

	out := graphOf(t, r.stdout)
	if _, ok := out.Node("repository:acme/checkout"); ok {
		t.Error("a repository node was added as well as the element that was named")
	}
	for _, e := range out.Edges {
		if e.Relation == "built_from" && e.To != "file:main.go" {
			t.Errorf("the edge points at %s", e.To)
		}
	}
}

func TestBuildRepoNamingNothingIsAnError(t *testing.T) {
	r := run(t, "", "graph", runningEstate(t),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=service:nowhere")
	if r.code == 0 {
		t.Fatal("an id naming nothing was accepted")
	}
	if !strings.Contains(r.stderr, "service:nowhere") {
		t.Errorf("the error does not name the id:\n%s", r.stderr)
	}
}

func TestBuildRepoWithoutRecordsIsAnError(t *testing.T) {
	r := run(t, "", "graph", runningEstate(t), "--build-repo", "acme/checkout=workload:shop/checkout")
	if r.code == 0 {
		t.Fatal("a mapping with nothing to map was accepted")
	}
}

func TestABadlyWrittenBuildRepoSaysHowToWriteIt(t *testing.T) {
	r := run(t, "", "graph", runningEstate(t),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout")
	if r.code == 0 {
		t.Fatal("a mapping with no = was accepted")
	}
	if !strings.Contains(r.stderr, "repository=id") {
		t.Errorf("the error does not say how to write it:\n%s", r.stderr)
	}
}

// The flag is on render as well, because a drawing is what most people run.
func TestRenderReadsBuildRecordsToo(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a.json")
	mustRun(t, "", "render", runningEstate(t), "--builds", buildsFile(t, buildRecord), "-o", out)

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "repository:acme/checkout") {
		t.Error("the repository did not reach the rendered graph")
	}
}

// The schema is the product, so a build record goes in the same front door as
// the other documents this project publishes a contract for.
func TestValidateReadsABuildRecord(t *testing.T) {
	r := mustRun(t, "", "validate", buildsFile(t, buildRecord))
	if !strings.Contains(r.stdout, "1 builds") {
		t.Errorf("validate said: %q", r.stdout)
	}
}

// A mapping for a repository no record mentions never gets consulted: the run
// still joins, to a node invented under the name somebody was overriding.
func TestBuildRepoForARepositoryNoRecordMentionsIsAnError(t *testing.T) {
	r := run(t, "", "graph", runningEstate(t),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/typo=workload:shop/checkout")
	if r.code == 0 {
		t.Fatal("a mapping for an unmentioned repository was accepted")
	}
	if !strings.Contains(r.stderr, "acme/typo") {
		t.Errorf("the error does not name the repository:\n%s", r.stderr)
	}
}

// The example in the error is what somebody copies, so it has to be the shape
// of an id that can exist rather than a scope prefix.
func TestTheSyntaxErrorShowsAnIdThatCouldExist(t *testing.T) {
	r := run(t, "", "graph", runningEstate(t),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout")
	if r.code == 0 {
		t.Fatal("a mapping with no = was accepted")
	}
	if !strings.Contains(r.stderr, "repo-1-checkout:") {
		t.Errorf("the example is not a whole id:\n%s", r.stderr)
	}
}

// Naming the input is how a whole repository is pointed at, which is what a
// drawing needs to open its code behind the box.
func TestBuildRepoCanNameAnInput(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-2-checkout", Path: "../checkout", Kind: "repository"}}}
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		// Stamped with the input it came from, the way combining inputs
		// stamps every node. An input that put nothing here is not something
		// a mapping can point at.
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-2-checkout"},
	}, {
		// An input is named so that its code can be opened, so an input with
		// code the map would draw is what the case is about.
		ID: "repo-2-checkout:file:main.go", Type: "code_file", Name: "main.go",
		Attrs: map[string]any{"repository": "repo-2-checkout"},
	}, {
		ID: "repo-2-checkout:package:net/http", Type: "code_package", Name: "net/http",
		Attrs: map[string]any{"repository": "repo-2-checkout"},
	}}
	g.Edges = []core.Edge{{
		From: "repo-2-checkout:file:main.go", To: "repo-2-checkout:package:net/http",
		Kind: core.EdgeIACRef, Relation: "imports",
	}}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-2-checkout")

	out := graphOf(t, r.stdout)
	repo, ok := out.Node("repository:acme/checkout")
	if !ok {
		t.Fatal("no repository node")
	}
	if repo.Attrs["code_input"] != "repo-2-checkout" {
		t.Errorf("the repository does not record which input its code is: %v", repo.Attrs)
	}
	for _, e := range out.Edges {
		if e.Relation == "built_from" && e.To != "repository:acme/checkout" {
			t.Errorf("the edge points at %s rather than at the repository", e.To)
		}
	}
}

// A graph read as an input brings its own input list along, and reading it back
// qualifies every id with the scope it was read under — the nodes that came
// out of that repository included. So an input of an input is a real input,
// with real code inside it, and naming one is the ordinary way to say where a
// repository's code is once an output has been read back.
//
// This was a test that the same mapping was refused. It passed on a fixture
// holding no code at all, which is a different refusal, and it would have gone
// on passing if nested inputs really did stop working.
func TestBuildRepoAcceptsAnInputOfAnInput(t *testing.T) {
	r := mustRun(t, "", "graph", graphFile(t, qualifiedEstate(t)),
		"--builds", buildsFile(t, buildRecord),
		"--build-repo", "acme/checkout=repo-1-out-json:repo-2-svc")

	repo, ok := graphOf(t, r.stdout).Node("repo-1-out-json:repository:acme/checkout")
	if !ok {
		t.Fatal("the repository that came in with the input is gone")
	}
	if of, _ := repo.Attrs["code_input"].(string); of != "repo-1-out-json:repo-2-svc" {
		t.Errorf("the input of an input landed as %q", of)
	}
}

// A repository node that arrived with the graph is still the repository this
// run was told about. Leaving the mapping off it kept whatever the earlier run
// said — or nothing at all — and the code map was then drawn from the wrong
// input, or never drawn.
func TestBuildRepoPlacesARepositoryThatIsAlreadyHere(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-2-checkout", Path: "../checkout", Kind: "repository"}}}
	g.Nodes = []core.Node{
		{
			ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-2-checkout"},
		},
		{
			ID: "repo-2-checkout:file:main.go", Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": "repo-2-checkout"},
		},
		{
			ID: "repo-2-checkout:package:net/http", Type: "code_package", Name: "net/http",
			Attrs: map[string]any{"repository": "repo-2-checkout"},
		},
		// Written by an earlier run, which knew a different estate.
		{
			ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"code_input": "repo-9-somewhere-else"},
		},
	}
	g.Edges = []core.Edge{{
		From: "repo-2-checkout:file:main.go", To: "repo-2-checkout:package:net/http",
		Kind: core.EdgeIACRef, Relation: "imports",
	}}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-2-checkout")

	out := graphOf(t, r.stdout)
	repo, ok := out.Node("repository:acme/checkout")
	if !ok {
		t.Fatal("no repository node")
	}
	if repo.Attrs["code_input"] != "repo-2-checkout" {
		t.Errorf("what this run was told did not hold: %v", repo.Attrs)
	}
}

// An input holding no code is a real input and still not something a
// repository's box can be opened onto. Recording the mapping anyway leaves it
// looking applied, which is what every other check here refuses.
func TestBuildRepoRefusesAnInputWithNoCode(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-1-cluster", Path: "cluster.yaml", Kind: "kubernetes"}}}
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-cluster"},
	}}
	g.Normalize()

	r := run(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-1-cluster")
	if r.code == 0 {
		t.Fatal("an input with no code was accepted as a repository's code")
	}
	if !strings.Contains(r.stderr, "no code map to draw") {
		t.Errorf("the error does not say why:\n%s", r.stderr)
	}
}

// An input id is a position in the run that wrote it, so a graph read back in
// as an input carries ids that mean something else here. Every other id is
// qualified on the way in; these two name inputs and are qualified with them.
//
// Left alone, `code_input: repo-2-…` came to mean the next run's own
// `repo-2-…`, and the code map drew that repository's code under this one's
// name.
func TestReadingAGraphBackInKeepsItsRepositoryPointingAtItsOwnCode(t *testing.T) {
	first := core.New()
	first.Nodes = []core.Node{
		{ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-2-checkout"}},
		{ID: "repo-2-checkout:file:main.go#Handle", Type: "code_function", Name: "Handle",
			Attrs: map[string]any{"repository": "repo-2-checkout"}},
		{ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"code_input": "repo-2-checkout"}},
	}
	first.Normalize()

	// Read back in beside something else, so the ids are qualified again and
	// the new run has a repo-2 of its own.
	other := core.New()
	other.Nodes = []core.Node{{
		ID: "file:billing.go#Charge", Type: "code_function", Name: "Charge",
		Attrs: map[string]any{"language": "go"},
	}}
	other.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, first), "--repo", graphFile(t, other))
	out := graphOf(t, r.stdout)

	repo, ok := out.Node("repo-1-graph-json:repository:acme/checkout")
	if !ok {
		var ids []string
		for _, n := range out.Nodes {
			ids = append(ids, n.ID)
		}
		t.Fatalf("no repository node among %v", ids)
	}
	of, _ := repo.Attrs["code_input"].(string)
	code, ok := out.Node("repo-1-graph-json:repo-2-checkout:file:main.go#Handle")
	if !ok {
		t.Fatal("the code came in under a different id than expected")
	}
	came, _ := code.Attrs["repository"].(string)
	if of != came {
		t.Errorf("the repository says its code is %q; its code says it came from %q", of, came)
	}
	// And it must not name an input of this run, which is a different thing
	// that happens to sit in the same position.
	if of == "repo-2-graph-json" {
		t.Error("the repository now points at the other input of this run")
	}
}

// A file that imports nothing is not on the code map, so an input holding only
// such files is an input the box cannot be opened onto. The check and the
// drawing have to agree about that, or the mapping is accepted and then draws
// nothing — the very outcome the check was added for.
func TestBuildRepoRefusesAnInputWhoseOnlyCodeTheMapWouldNotDraw(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-1-docs", Path: "../docs", Kind: "repository"}}}
	g.Nodes = []core.Node{
		{ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-docs"}},
		// A file, but one that imports nothing, so the map keeps no box for it.
		{ID: "repo-1-docs:file:README.md", Type: "code_file", Name: "README.md",
			Attrs: map[string]any{"repository": "repo-1-docs"}},
	}
	g.Normalize()

	r := run(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-1-docs")
	if r.code == 0 {
		t.Fatal("an input the map would draw nothing from was accepted")
	}
	if !strings.Contains(r.stderr, "no code map to draw") {
		t.Errorf("the error does not say why:\n%s", r.stderr)
	}
}

// Pointing the repository at an element instead is a replacement, not an
// addition. The answer the earlier run wrote is the answer to a question
// nobody asked any more, and leaving it standing put a second door on the
// workload — opening onto the code map of the mapping just replaced.
func TestPointingARepositoryAtAnElementDropsTheOldCodeMap(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-2-svc", Path: "../svc", Kind: "repository"}}}
	g.Nodes = []core.Node{
		{ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-2-svc"}},
		{ID: "repo-2-svc:file:main.go", Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repo-2-svc:package:net/http", Type: "code_package", Name: "net/http",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		// Written by the run that said the repository is the whole input.
		{ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"code_input": "repo-2-svc"}},
	}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-2-svc:file:main.go")

	out := graphOf(t, r.stdout)
	repo, ok := out.Node("repository:acme/checkout")
	if !ok {
		return // dropped entirely is an honest outcome too
	}
	if of, found := repo.Attrs["code_input"]; found {
		t.Errorf("the replaced mapping is still on the repository: %v", of)
	}
}

// qualifiedEstate is what a previous output looks like on the way back in:
// everything wearing the scope it was read under, the repository node
// included. The id `repository:acme/checkout` is not in it, and the repository
// is still in it.
func qualifiedEstate(t *testing.T) *core.Graph {
	t.Helper()
	const scope = "repo-1-out-json:repo-2-svc"
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: scope, Path: "../svc", Kind: "repository"}}}
	g.Nodes = []core.Node{
		{ID: "repo-1-out-json:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-out-json"}},
		{ID: scope + ":file:main.go", Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": scope}},
		{ID: scope + ":package:net/http", Type: "code_package", Name: "net/http",
			Attrs: map[string]any{"repository": scope}},
		{ID: "repo-1-out-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-out-json", "code_input": scope}},
	}
	g.Edges = []core.Edge{{
		From: scope + ":file:main.go", To: scope + ":package:net/http",
		Kind: core.EdgeIACRef, Relation: "imports",
	}}
	g.Normalize()
	return g
}

func repositoryNodes(g *core.Graph, name string) []string {
	var out []string
	for _, n := range g.Nodes {
		if n.Type == "repository" && n.Name == name {
			out = append(out, n.ID)
		}
	}
	return out
}

// The repository a previous output carries is the same repository, wearing the
// scope it was read under. Looking for it at the id this run would have given
// it found nothing and invented a second box — two boxes for one repository,
// disagreeing about whether it has code.
func TestARepositoryThatCameBackQualifiedIsNotInventedAgain(t *testing.T) {
	r := mustRun(t, "", "graph", graphFile(t, qualifiedEstate(t)),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-1-out-json:repo-2-svc")

	out := graphOf(t, r.stdout)
	ids := repositoryNodes(out, "acme/checkout")
	if len(ids) != 1 {
		t.Fatalf("%d boxes for one repository: %v", len(ids), ids)
	}
	if ids[0] != "repo-1-out-json:repository:acme/checkout" {
		t.Errorf("the edge points at %q rather than at the repository already here", ids[0])
	}
	for _, e := range out.Edges {
		if e.Relation == "built_from" && e.To != ids[0] {
			t.Errorf("a built_from edge points at %s", e.To)
		}
	}
}

// A box that came in from a previous output is open onto the code that output
// found. Pointing the same repository at an element says it has no code map,
// and that is a sentence about this box: the element is inside the input the
// box came from. Leaving the old answer standing kept the atlas opening the
// input the operator had just stopped naming.
func TestPointingAtAnElementClosesTheBoxItIsInside(t *testing.T) {
	r := mustRun(t, "", "graph", graphFile(t, qualifiedEstate(t)),
		"--builds", buildsFile(t, buildRecord),
		"--build-repo", "acme/checkout=repo-1-out-json:repo-2-svc:file:main.go")

	out := graphOf(t, r.stdout)
	repo, ok := out.Node("repo-1-out-json:repository:acme/checkout")
	if !ok {
		t.Fatal("the repository that came in with the input is gone")
	}
	if of, found := repo.Attrs["code_input"]; found {
		t.Errorf("the box is still open onto %v", of)
	}
}

// An input that parsed to nothing is still an input. Being told it is not one
// sends the reader looking for a typo in an id the metadata lists.
func TestAnInputThatHoldsNothingIsToldWhatIsActuallyWrong(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: "repo-1-cluster", Path: "cluster.yaml", Kind: "kubernetes"},
		{ID: "repo-2-empty", Path: "../empty", Kind: "repository"},
	}}
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-cluster"},
	}}
	g.Normalize()

	r := run(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-2-empty")
	if r.code == 0 {
		t.Fatal("an input holding nothing was accepted")
	}
	if strings.Contains(r.stderr, "nothing here is") {
		t.Errorf("a listed input was called not an input:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, "no code map to draw") {
		t.Errorf("the error does not say what is wrong:\n%s", r.stderr)
	}
}

// Combining two outputs leaves a box per input, each already answering about
// the code inside its own input. A mapping names one input, so writing it on
// every box that shares the name makes the others claim code that lives
// somewhere else — and puts the code they did have out of reach of every page.
func TestAMappingDoesNotClobberAnotherInputsAnswer(t *testing.T) {
	const a, b = "repo-1-a-json:repo-2-svc", "repo-2-b-json:repo-3-svc"
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: a, Path: "../svc", Kind: "repository"},
		{ID: b, Path: "../svc", Kind: "repository"},
	}}
	g.Nodes = []core.Node{
		{ID: "repo-1-a-json:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-a-json"}},
		{ID: "repo-1-a-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-a-json", "code_input": a}},
		{ID: "repo-2-b-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-2-b-json", "code_input": b}},
	}
	for _, scope := range []string{a, b} {
		g.Nodes = append(g.Nodes,
			core.Node{ID: scope + ":file:main.go", Type: "code_file", Name: "main.go",
				Attrs: map[string]any{"repository": scope}},
			core.Node{ID: scope + ":package:net/http", Type: "code_package", Name: "net/http",
				Attrs: map[string]any{"repository": scope}})
		g.Edges = append(g.Edges, core.Edge{
			From: scope + ":file:main.go", To: scope + ":package:net/http",
			Kind: core.EdgeIACRef, Relation: "imports",
		})
	}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout="+a)

	out := graphOf(t, r.stdout)
	for id, want := range map[string]string{
		"repo-1-a-json:repository:acme/checkout": a,
		"repo-2-b-json:repository:acme/checkout": b,
	} {
		n, ok := out.Node(id)
		if !ok {
			t.Errorf("%s is gone", id)
			continue
		}
		if of, _ := n.Attrs["code_input"].(string); of != want {
			t.Errorf("%s says its code is %q, want %q", id, of, want)
		}
	}
	// And the join points at the box from the same input as the workload,
	// rather than at whichever box sorts first.
	for _, e := range out.Edges {
		if e.Relation == "built_from" && e.To != "repo-1-a-json:repository:acme/checkout" {
			t.Errorf("the deployment is drawn as built from %s as well", e.To)
		}
	}
}

// The estate has moved on to a tag no record covers, so nothing here matches
// and no edge is drawn. The mapping still says what it says: this repository
// is that element. Doing the replacement only where a record matched left the
// workload's box open onto the code map of the mapping just replaced.
func TestReplacingAMappingHoldsEvenWhenNoRecordMatches(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-2-svc", Path: "../svc", Kind: "repository"}}}
	g.Nodes = []core.Node{
		// Running 1.3.0; the record below is about 1.4.0.
		{ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.3.0", "repository": "repo-2-svc"}},
		{ID: "repo-2-svc:file:main.go", Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repo-2-svc:package:net/http", Type: "code_package", Name: "net/http",
			Attrs: map[string]any{"repository": "repo-2-svc"}},
		{ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"code_input": "repo-2-svc"}},
	}
	g.Edges = []core.Edge{{
		From: "repo-2-svc:file:main.go", To: "repo-2-svc:package:net/http",
		Kind: core.EdgeIACRef, Relation: "imports",
	}}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord),
		"--build-repo", "acme/checkout=repo-2-svc:file:main.go")

	out := graphOf(t, r.stdout)
	repo, ok := out.Node("repository:acme/checkout")
	if !ok {
		return
	}
	if of, found := repo.Attrs["code_input"]; found {
		t.Errorf("the replaced mapping is still on the repository: %v", of)
	}
}

// The estate has moved on to a tag no record covers, so nothing matches and no
// edge is drawn. The mapping still says what it says. Writing the answer only
// where a record matched left the repository pointing at the input the
// operator had just stopped naming.
func TestRepointingAMappingHoldsEvenWhenNoRecordMatches(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: "repo-2-svc", Path: "../svc", Kind: "repository"},
		{ID: "repo-3-other", Path: "../other", Kind: "repository"},
	}}
	g.Nodes = []core.Node{
		// Running 1.3.0; the record below is about 1.4.0.
		{ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.3.0", "repository": "repo-2-svc"}},
		{ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"code_input": "repo-2-svc"}},
	}
	for _, scope := range []string{"repo-2-svc", "repo-3-other"} {
		g.Nodes = append(g.Nodes,
			core.Node{ID: scope + ":file:main.go", Type: "code_file", Name: "main.go",
				Attrs: map[string]any{"repository": scope}},
			core.Node{ID: scope + ":package:net/http", Type: "code_package", Name: "net/http",
				Attrs: map[string]any{"repository": scope}})
		g.Edges = append(g.Edges, core.Edge{
			From: scope + ":file:main.go", To: scope + ":package:net/http",
			Kind: core.EdgeIACRef, Relation: "imports",
		})
	}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-3-other")

	out := graphOf(t, r.stdout)
	repo, ok := out.Node("repository:acme/checkout")
	if !ok {
		t.Fatal("no repository node")
	}
	if of, _ := repo.Attrs["code_input"].(string); of != "repo-3-other" {
		t.Errorf("the repository still says its code is %q", of)
	}
}

// A mapping naming an input outside the one a box came from is not about that
// box, and is not written there — which is right, and silent. The run said the
// mapping was applied while the box went on opening onto what it opened onto
// before.
func TestAMappingThatReachesNoBoxIsRefusedRatherThanReported(t *testing.T) {
	const old, other = "repo-1-prev-json:repo-9-old", "repo-2-checkout"
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: old, Path: "../old", Kind: "repository"},
		{ID: other, Path: "../checkout", Kind: "repository"},
	}}
	g.Nodes = []core.Node{
		{ID: "repo-1-prev-json:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": old}},
		{ID: "repo-1-prev-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-prev-json", "code_input": old}},
		// A second box, so the mapping has something to be told apart from —
		// and neither of them came from the input it names.
		{ID: "repo-3-third-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-3-third-json"}},
	}
	for _, scope := range []string{old, other} {
		g.Nodes = append(g.Nodes,
			core.Node{ID: scope + ":file:main.go", Type: "code_file", Name: "main.go",
				Attrs: map[string]any{"repository": scope}},
			core.Node{ID: scope + ":package:net/http", Type: "code_package", Name: "net/http",
				Attrs: map[string]any{"repository": scope}})
		g.Edges = append(g.Edges, core.Edge{
			From: scope + ":file:main.go", To: scope + ":package:net/http",
			Kind: core.EdgeIACRef, Relation: "imports",
		})
	}
	g.Normalize()

	r := run(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout="+other)
	if r.code == 0 {
		t.Fatal("a mapping that reached no box was accepted and reported as applied")
	}
	if !strings.Contains(r.stderr, "changed nothing") {
		t.Errorf("the error does not say what happened:\n%s", r.stderr)
	}
}

// The same mapping, in a run that draws no atlas. What a box opens onto is
// drawn by an atlas and by nothing else, so a single drawing that could never
// have shown it is refused over nothing — and the code lines that answer the
// question can be denied by an overlay, which is a person saying those lines
// are not there rather than a mistake to stop the run over.
func TestADrawingWithNoAtlasIsNotRefusedOverACodeMap(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-1-cluster", Path: "cluster.yaml", Kind: "kubernetes"}}}
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-cluster"},
	}}
	g.Normalize()
	path := graphFile(t, g)
	records := buildsFile(t, buildRecord)

	mustRun(t, "", "render", path, "-f", "svg",
		"--builds", records, "--build-repo", "acme/checkout=repo-1-cluster")

	// The page that would have drawn it still asks.
	r := run(t, "", "render", path, "-f", "html", "--atlas",
		"--builds", records, "--build-repo", "acme/checkout=repo-1-cluster")
	if r.code == 0 {
		t.Fatal("the atlas drew a box opening onto an input with no code")
	}
	if !strings.Contains(r.stderr, "no code map to draw") {
		t.Errorf("the error does not say why:\n%s", r.stderr)
	}
}

// The documented command, run on its own output. The repository comes back
// qualified with the scope it was read under and carrying what the first run
// said its code was; the code read this time is a fresh input with an id of
// its own. Refusing to hear the same flag a second time left the run with a
// box whose answer named the first run's copy, and stopped it altogether.
func TestTheSameCommandOnItsOwnOutputIsHeard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const source = "import json\n\ndef handle():\n  return total()\n\ndef total():\n  return json\n"
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	g := core.New()
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0"},
	}}
	g.Normalize()

	records := buildsFile(t, buildRecord)
	flags := []string{"--repo", dir, "--builds", records, "--build-repo", "acme/checkout=repo-2-checkout"}

	first := mustRun(t, "", append([]string{"graph", graphFile(t, g)}, flags...)...).stdout
	out1 := filepath.Join(t.TempDir(), "out1.json")
	if err := os.WriteFile(out1, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}

	again := mustRun(t, "", append([]string{"graph", out1}, flags...)...).stdout

	out := graphOf(t, again)
	var boxes []*core.Node
	for i := range out.Nodes {
		if out.Nodes[i].Type == "repository" && out.Nodes[i].Name == "acme/checkout" {
			boxes = append(boxes, &out.Nodes[i])
		}
	}
	if len(boxes) != 1 {
		t.Fatalf("%d boxes for one repository", len(boxes))
	}
	if of, _ := boxes[0].Attrs["code_input"].(string); of != "repo-2-checkout" {
		t.Errorf("the box opens %q rather than the code this run read", of)
	}
}

// A graph is the answer carried onward rather than a picture of it, whichever
// command wrote it. `render -f json` writes one, so the mapping it records
// reaches whoever renders that file next, and the question has to be asked
// while it can still be answered.
func TestAGraphWrittenByRenderIsCheckedLikeAnyOther(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-1-cluster", Path: "cluster.yaml", Kind: "kubernetes"}}}
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-cluster"},
	}}
	g.Normalize()

	r := run(t, "", "render", graphFile(t, g), "-f", "json",
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-1-cluster")
	if r.code == 0 {
		t.Fatal("a graph was written recording a mapping with no code map to open")
	}
	if !strings.Contains(r.stderr, "no code map to draw") {
		t.Errorf("the error does not say why:\n%s", r.stderr)
	}
}
