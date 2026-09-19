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

// The same replacement as before, against the id a previous output actually
// uses. The mapping was accepted, the old answer stayed, and the workload kept
// a door onto the code map of the mapping just replaced.
func TestPointingAQualifiedRepositoryAtAnElementDropsTheOldCodeMap(t *testing.T) {
	r := mustRun(t, "", "graph", graphFile(t, qualifiedEstate(t)),
		"--builds", buildsFile(t, buildRecord),
		"--build-repo", "acme/checkout=repo-1-out-json:repo-2-svc:file:main.go")

	out := graphOf(t, r.stdout)
	repo, ok := out.Node("repo-1-out-json:repository:acme/checkout")
	if !ok {
		return // dropped entirely is an honest outcome too
	}
	if of, found := repo.Attrs["code_input"]; found {
		t.Errorf("the replaced mapping is still on the repository: %v", of)
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

// Combining two outputs leaves two boxes for one repository. Telling only the
// first where its code is leaves the second answering the same question
// differently — and the one that kept the old answer opens onto the code of a
// mapping nobody made this time.
func TestEveryBoxForOneRepositoryGetsTheSameAnswer(t *testing.T) {
	g := core.New()
	const scope = "repo-1-a-json:repo-2-svc"
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: scope, Path: "../svc", Kind: "repository"}}}
	g.Nodes = []core.Node{
		{ID: "repo-1-a-json:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{"image": "registry.example/checkout:1.4.0", "repository": "repo-1-a-json"}},
		{ID: scope + ":file:main.go", Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": scope}},
		{ID: scope + ":package:net/http", Type: "code_package", Name: "net/http",
			Attrs: map[string]any{"repository": scope}},
		// Two boxes for one repository, from two outputs combined.
		{ID: "repo-1-a-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-a-json", "code_input": "repo-9-stale"}},
		{ID: "repo-2-b-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-2-b-json", "code_input": "repo-9-stale"}},
	}
	g.Edges = []core.Edge{{
		From: scope + ":file:main.go", To: scope + ":package:net/http",
		Kind: core.EdgeIACRef, Relation: "imports",
	}}
	g.Normalize()

	r := mustRun(t, "", "graph", graphFile(t, g),
		"--builds", buildsFile(t, buildRecord), "--build-repo", "acme/checkout="+scope)

	out := graphOf(t, r.stdout)
	for _, id := range repositoryNodes(out, "acme/checkout") {
		n, _ := out.Node(id)
		if of, _ := n.Attrs["code_input"].(string); of != scope {
			t.Errorf("%s says its code is %q", id, of)
		}
	}
}
