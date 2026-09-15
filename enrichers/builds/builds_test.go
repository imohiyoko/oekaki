package builds

import (
	"testing"

	"github.com/imohiyoko/oekaki/collectors/builds"
	"github.com/imohiyoko/oekaki/core"
)

func graphRunning(image string) *core.Graph {
	g := core.New()
	g.Nodes = []core.Node{{
		ID: "workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
		Attrs: map[string]any{"image": image},
	}}
	return g
}

func record(t *testing.T, doc string) *builds.Document {
	t.Helper()
	d, err := builds.Parse([]byte(doc), "builds.json")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const oneBuild = `{
  "kind": "oekaki.builds", "version": "0.1",
  "builds": [{
    "repository": "acme/checkout",
    "commit": "9f1c0f2e",
    "run": { "id": "17243", "workflow": "release", "url": "https://ci.example/17243" },
    "images": [{ "reference": "registry.example/checkout:1.4.0", "digest": "sha256:aaaa" }]
  }]
}`

func TestTheReferenceIsTheJoin(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.0")
	r, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 {
		t.Fatalf("applied %d: %+v", r.Applied, r)
	}

	var found *core.Edge
	for i, e := range g.Edges {
		if e.Relation == Relation {
			found = &g.Edges[i]
		}
	}
	if found == nil {
		t.Fatal("no built_from edge")
	}
	if found.From != "workload:shop/checkout" || found.To != "repository:acme/checkout" {
		t.Errorf("edge runs %s -> %s", found.From, found.To)
	}
	if found.Kind != core.EdgeObserved {
		t.Errorf("kind = %q; a build record is something that happened", found.Kind)
	}
	// The claim has to name the run, or the join is an assertion with no
	// author and no reason.
	if found.Claim == nil || found.Claim.Note != "release run 17243" {
		t.Errorf("claim = %+v", found.Claim)
	}
	if found.Attrs["commit"] != "9f1c0f2e" || found.Attrs["run_url"] != "https://ci.example/17243" {
		t.Errorf("attrs = %v", found.Attrs)
	}

	repo, ok := g.Node("repository:acme/checkout")
	if !ok {
		t.Fatal("no repository node")
	}
	// A box that was not in the estate a moment ago has to be reported as one.
	if len(r.Adopted) != 1 || r.Adopted[0] != "repository:acme/checkout" {
		t.Errorf("adopted = %v", r.Adopted)
	}
	if repo.Name != "acme/checkout" || repo.Type != NodeRepository {
		t.Errorf("repository node = %+v", repo)
	}
	if err := g.Validate(); err != nil {
		t.Errorf("the graph no longer validates: %v", err)
	}
}

// An estate that pins by digest never writes the tag the record was pushed
// with, so the reference alone would find nothing.
func TestADigestJoinsToo(t *testing.T) {
	g := graphRunning("registry.example/checkout@sha256:aaaa")
	r, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 {
		t.Fatalf("applied %d: %+v", r.Applied, r)
	}
}

// Nothing is stripped, completed or normalised: a tag that differs is a
// different image, and guessing otherwise is the whole thing being avoided.
func TestALikelyLookingImageIsNotJoined(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.1")
	r, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 0 {
		t.Fatalf("applied %d", r.Applied)
	}
	if len(r.Unmatched) != 1 || r.Unmatched[0].Reason == "" {
		t.Fatalf("unmatched = %+v", r.Unmatched)
	}
	if _, ok := g.Node("repository:acme/checkout"); ok {
		t.Error("a repository nothing joined to was added anyway")
	}
}

// One image, two repositories: one of them did not build it, and nothing here
// knows which.
func TestTwoRepositoriesClaimingOneImageAreNotPickedBetween(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [
        { "repository": "acme/checkout", "run": { "id": "1" },
          "images": [{ "reference": "registry.example/checkout:1.4.0" }] },
        { "repository": "acme/legacy-checkout", "run": { "id": "2" },
          "images": [{ "reference": "registry.example/checkout:1.4.0" }] }
      ]
    }`
	g := graphRunning("registry.example/checkout:1.4.0")
	r, err := (Enricher{Documents: []*builds.Document{record(t, doc)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 0 {
		t.Fatalf("applied %d", r.Applied)
	}
	if len(r.Ambiguous) != 1 || len(r.Ambiguous[0].Candidates) != 2 {
		t.Fatalf("ambiguous = %+v", r.Ambiguous)
	}
	if r.Ambiguous[0].Candidates[0] != "acme/checkout" {
		t.Errorf("candidates are not in a canonical order: %v", r.Ambiguous[0].Candidates)
	}
}

// Rebuilding a tag is ordinary. The later run wins, the same way every time.
func TestTheLaterRunOfOneRepositoryWins(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [
        { "repository": "acme/checkout", "commit": "old",
          "run": { "id": "1", "completed_at": "2026-09-01T00:00:00Z" },
          "images": [{ "reference": "registry.example/checkout:latest" }] },
        { "repository": "acme/checkout", "commit": "new",
          "run": { "id": "2", "completed_at": "2026-09-08T00:00:00Z" },
          "images": [{ "reference": "registry.example/checkout:latest" }] }
      ]
    }`
	for _, name := range []string{"first pass", "second pass"} {
		t.Run(name, func(t *testing.T) {
			g := graphRunning("registry.example/checkout:latest")
			if _, err := (Enricher{Documents: []*builds.Document{record(t, doc)}}).Enrich(g); err != nil {
				t.Fatal(err)
			}
			if got := g.Edges[0].Attrs["commit"]; got != "new" {
				t.Errorf("commit = %v, want the later run's", got)
			}
		})
	}
}

// Whoever knows the estate can say which element is that repository, and then
// the edge points at what is already drawn instead of at a new box.
func TestAWrittenDownRepositoryIsUsedInstead(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Nodes = append(g.Nodes, core.Node{ID: "file:main.go", Type: "code_file", Name: "main.go"})

	r, err := Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": "file:main.go"},
	}.Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 {
		t.Fatalf("applied %d", r.Applied)
	}
	if g.Edges[0].To != "file:main.go" {
		t.Errorf("edge points at %s", g.Edges[0].To)
	}
	if len(r.Adopted) != 0 {
		t.Errorf("nothing was invented, but adopted = %v", r.Adopted)
	}
	if _, ok := g.Node("repository:acme/checkout"); ok {
		t.Error("a repository node was added as well as the element that was named")
	}
}

func TestANodeWithNoImageIsLeftAlone(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "service:a", Type: "service", Name: "a"}}
	r, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 0 || len(g.Edges) != 0 {
		t.Fatalf("report = %+v, edges = %v", r, g.Edges)
	}
}
