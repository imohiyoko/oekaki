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

// A graph read as an input brings its own input list along. Those ids name
// documents the graph it came from was built out of, and nothing here was ever
// stamped with one — so a mapping naming one passes every check and then
// matches nothing, which is the silent no-op the checks exist to prevent.
func TestBuildRepoRefusesAnInputOfAnInput(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: "repo-1-prev", Path: "previous.json", Kind: "repository"},
		{ID: "repo-1-prev:repo-2-old", Path: "../old", Kind: "repository"},
	}}
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-prev"},
	}}
	g.Normalize()

	r := run(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout=repo-1-prev:repo-2-old")
	if r.code == 0 {
		t.Fatal("an input of an input was accepted as a place to point at")
	}
	if !strings.Contains(r.stderr, "repo-1-prev:repo-2-old") {
		t.Errorf("the error does not name the id:\n%s", r.stderr)
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
		// Written by an earlier run, which knew a different estate.
		{
			ID: "repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"code_input": "repo-9-somewhere-else"},
		},
	}
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
