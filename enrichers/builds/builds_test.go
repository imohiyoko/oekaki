package builds

import (
	"strings"
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

// One build pushing a tag and :latest at one digest, joined to a workload that
// pins the digest. The estate runs that image; saying nothing does is the
// opposite of true in the one line a reader acts on.
func TestAnImageRunUnderAnotherOfItsNamesIsNotReportedMissing(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [{
        "repository": "acme/checkout",
        "run": { "id": "1" },
        "images": [
          { "reference": "registry.example/checkout:1.4.0", "digest": "sha256:aaaa" },
          { "reference": "registry.example/checkout:latest", "digest": "sha256:aaaa" }
        ]
      }]
    }`
	g := graphRunning("registry.example/checkout@sha256:aaaa")
	r, err := (Enricher{Documents: []*builds.Document{record(t, doc)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 {
		t.Fatalf("applied %d", r.Applied)
	}
	if len(r.Unmatched) != 0 {
		t.Errorf("reported as missing anyway: %+v", r.Unmatched)
	}
}

// Two repositories claiming one tag at one digest contest both the reference
// and the digest. It is one conflict, and a second line under a bare sha256
// reads as another one to go and investigate.
func TestOneConflictIsSaidOnce(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [
        { "repository": "acme/checkout", "run": { "id": "1" },
          "images": [{ "reference": "registry.example/checkout:1.4.0", "digest": "sha256:aaaa" }] },
        { "repository": "acme/legacy-checkout", "run": { "id": "2" },
          "images": [{ "reference": "registry.example/checkout:1.4.0", "digest": "sha256:aaaa" }] }
      ]
    }`
	g := graphRunning("registry.example/checkout:1.4.0")
	r, err := (Enricher{Documents: []*builds.Document{record(t, doc)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Ambiguous) != 1 {
		t.Fatalf("ambiguous = %+v", r.Ambiguous)
	}
	if got := r.Ambiguous[0].Selector["image"]; got != "registry.example/checkout:1.4.0" {
		t.Errorf("reported under %q rather than the reference", got)
	}
}

// Two repositories pushing one digest under different tags are two facts, and
// both are worth a line.
func TestADigestContestedOnItsOwnIsStillSaid(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [
        { "repository": "acme/checkout", "run": { "id": "1" },
          "images": [{ "reference": "registry.example/checkout:1.4.0", "digest": "sha256:aaaa" }] },
        { "repository": "acme/other", "run": { "id": "2" },
          "images": [{ "reference": "registry.example/other:9", "digest": "sha256:aaaa" }] }
      ]
    }`
	g := graphRunning("registry.example/checkout@sha256:aaaa")
	r, err := (Enricher{Documents: []*builds.Document{record(t, doc)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Ambiguous) != 1 || r.Ambiguous[0].Selector["image"] != "sha256:aaaa" {
		t.Fatalf("ambiguous = %+v", r.Ambiguous)
	}
	if r.Applied != 0 {
		t.Errorf("applied %d: a contested digest should join to nothing", r.Applied)
	}
}

// Reading a graph this already ran on is ordinary: the repository node it
// wrote then is the same repository now, and reusing it is right.
func TestARepositoryNodeAlreadyHereIsReused(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Nodes = append(g.Nodes, core.Node{
		ID: "repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
	})

	r, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 || len(r.Adopted) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if got := len(g.Nodes); got != 2 {
		t.Errorf("%d nodes: the repository was added a second time", got)
	}
}

// Something else wearing that id is a different thing with the same name. The
// graph would still validate, so nothing downstream could tell.
func TestSomethingElseWearingTheRepositoryIdIsRefused(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Nodes = append(g.Nodes, core.Node{
		ID: "repository:acme/checkout", Type: "service", Name: "a service somebody named oddly",
	})

	_, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g)
	if err == nil {
		t.Fatal("the edge was pointed at somebody else's box")
	}
	if !strings.Contains(err.Error(), "repository:acme/checkout") {
		t.Errorf("the error does not name the id:\n%v", err)
	}
}

// Two builds of one tag at two digests are two images. The estate running the
// older one is not a reason to stay quiet about the newer.
func TestOneTagRebuiltAtANewDigestIsStillReportedMissing(t *testing.T) {
	doc := `{
      "kind": "oekaki.builds", "version": "0.1",
      "builds": [
        { "repository": "acme/checkout", "run": { "id": "1", "completed_at": "2026-09-01T00:00:00Z" },
          "images": [{ "reference": "registry.example/checkout:prod", "digest": "sha256:old" }] },
        { "repository": "acme/checkout", "run": { "id": "2", "completed_at": "2026-09-08T00:00:00Z" },
          "images": [{ "reference": "registry.example/checkout:prod", "digest": "sha256:new" }] }
      ]
    }`
	g := graphRunning("registry.example/checkout@sha256:old")
	r, err := (Enricher{Documents: []*builds.Document{record(t, doc)}}).Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 {
		t.Fatalf("applied %d: the pinned digest should still join", r.Applied)
	}
	if len(r.Unmatched) != 1 {
		t.Fatalf("unmatched = %+v; the digest running here is not the one that tag now means", r.Unmatched)
	}
}

// Which ids name a whole repository is read from the graph rather than handed
// in. It used to be a field with no unset state, so a caller who left it out
// got every mapping read as an element: the edge pointed at an id no node
// wears, and what the last run recorded was erased.
func TestNamingAnInputWorksWithoutBeingToldWhatTheInputsAre(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{{ID: "repo-2-svc", Path: "../svc", Kind: "repository"}}}

	r, err := Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": "repo-2-svc"},
	}.Enrich(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 {
		t.Fatalf("applied %d", r.Applied)
	}
	if g.Edges[0].To != "repository:acme/checkout" {
		t.Errorf("the edge points at %s rather than at a node for the repository", g.Edges[0].To)
	}
	n, ok := g.Node("repository:acme/checkout")
	if !ok {
		t.Fatal("no repository node")
	}
	if of, _ := n.Attrs[AttrCodeInput].(string); of != "repo-2-svc" {
		t.Errorf("the repository does not record which input its code is: %v", n.Attrs)
	}
}

// Reading a combined output back in. The box this enricher invented had no
// input attribute, so it is stamped with the outer scope alone while the
// workload beside it keeps a nested one — and demanding the two be equal
// missed the box that was right there and invented a bare one next to it.
func TestACombinedOutputReadBackDoesNotDoubleTheRepository(t *testing.T) {
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: "repo-1-step1-json", Path: "step1.json", Kind: "graph"},
		{ID: "repo-1-step1-json:repo-2-checkout", Path: "../checkout", Kind: "repository"},
	}}
	g.Nodes = []core.Node{
		{ID: "repo-1-step1-json:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{
				"image":      "registry.example/checkout:1.4.0",
				"repository": "repo-1-step1-json:repo-2-checkout",
			}},
		// Invented by the earlier run, so it wore no input attribute then and
		// wears the outer scope now.
		{ID: "repo-1-step1-json:repository:acme/checkout", Type: "repository", Name: "acme/checkout",
			Attrs: map[string]any{
				"repository": "repo-1-step1-json",
				"code_input": "repo-1-step1-json:repo-2-checkout",
			}},
	}
	g.Normalize()

	if _, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	var boxes []string
	for _, n := range g.Nodes {
		if n.Type == NodeRepository && n.Name == "acme/checkout" {
			boxes = append(boxes, n.ID)
		}
	}
	if len(boxes) != 1 {
		t.Fatalf("%d boxes for one repository: %v", len(boxes), boxes)
	}
	if g.Edges[0].To != "repo-1-step1-json:repository:acme/checkout" {
		t.Errorf("the edge points at %s rather than at the box already here", g.Edges[0].To)
	}
}

// A box of input B taking A's answer puts B's own code out of reach of every
// page, and it does that just as thoroughly when B had said nothing yet.
func TestAnUnansweredBoxOfAnotherInputIsNotGivenThisOnesCode(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: "repo-1-outa-json:repo-2-svca", Path: "../a", Kind: "repository"},
		{ID: "repo-2-outb-json:repo-3-svcb", Path: "../b", Kind: "repository"},
	}}
	g.Nodes = append(g.Nodes,
		core.Node{ID: "repo-1-outa-json:repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-outa-json"}},
		core.Node{ID: "repo-2-outb-json:repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-2-outb-json"}})
	g.Normalize()

	if _, err := (Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": "repo-1-outa-json:repo-2-svca"},
	}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	b, ok := g.Node("repo-2-outb-json:repository:acme/checkout")
	if !ok {
		t.Fatal("the other input's box is gone")
	}
	if of, found := b.Attrs[AttrCodeInput]; found {
		t.Errorf("the other input's box was given %v", of)
	}
	a, _ := g.Node("repo-1-outa-json:repository:acme/checkout")
	if of, _ := a.Attrs[AttrCodeInput].(string); of != "repo-1-outa-json:repo-2-svca" {
		t.Errorf("the box the mapping is about says %q", of)
	}
}

// One box, carrying what an earlier run said its code was, and this run saying
// something else. This run is heard: it is a sentence said out loud now, about
// a repository with one box in this estate, and there is nothing here to tell
// that box apart from.
//
// This test used to assert the opposite — that an answer already there was
// never replaced. That protection is what `owned` and the count of boxes are
// for, and both of them still hold; refusing the single box as well meant the
// documented command failed when it was run on its own output, where the box
// comes back qualified and the code read this time does not.
func TestTheOnlyBoxTakesThisRunsWord(t *testing.T) {
	const its = "repo-1-a-json:repo-2-svc"
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: its, Path: "../svc", Kind: "repository"},
		{ID: "repo-2-checkout", Path: "../checkout", Kind: "repository"},
	}}
	g.Nodes = append(g.Nodes, core.Node{
		ID: "repo-1-a-json:repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
		Attrs: map[string]any{"repository": its, "code_input": its},
	})
	g.Normalize()

	if _, err := (Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": "repo-2-checkout"},
	}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	n, _ := g.Node("repo-1-a-json:repository:acme/checkout")
	if of, _ := n.Attrs[AttrCodeInput].(string); of != "repo-2-checkout" {
		t.Errorf("what this run said was not heard: the box says %q", of)
	}
}

// A graph somebody hands in may carry the bare id while saying it came from an
// input that covers nothing here. Preferring that box would be wrong;
// refusing the whole run over it is worse, and saying it cannot be told apart
// from the repository the record names is not true of it.
func TestABareRepositoryFromSomeOtherInputIsUsedRatherThanRefused(t *testing.T) {
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Nodes = append(g.Nodes, core.Node{
		ID: "repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
		Attrs: map[string]any{"repository": "repo-9-elsewhere"},
	})
	g.Normalize()

	r, err := (Enricher{Documents: []*builds.Document{record(t, oneBuild)}}).Enrich(g)
	if err != nil {
		t.Fatalf("a repository already here was refused: %v", err)
	}
	if r.Applied != 1 {
		t.Fatalf("applied %d", r.Applied)
	}
	if g.Edges[0].To != "repository:acme/checkout" {
		t.Errorf("the edge points at %s", g.Edges[0].To)
	}
}

// A mapping pointed at an element says this repository has no code map here,
// and the answer an earlier run wrote has to go. Every box that came out of
// that run wears the input it was read from, so clearing only the boxes with
// nothing to say about where they came from cleared none of them: the box
// stayed open onto the input the operator had just stopped naming.
func TestPointingAtAnElementRetractsWhatAnEarlierRunWrote(t *testing.T) {
	const out = "repo-1-prev-json"
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: out, Path: "prev.json", Kind: "graph"},
		{ID: out + ":repo-2-checkout", Path: "../checkout", Kind: "repository"},
	}}
	g.Nodes = append(g.Nodes,
		core.Node{ID: out + ":repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": out, "code_input": out + ":repo-2-checkout"}},
		core.Node{ID: out + ":file:main.go", Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": out}})
	g.Normalize()

	if _, err := (Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": out + ":file:main.go"},
	}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	n, _ := g.Node(out + ":repository:acme/checkout")
	if of, found := n.Attrs[AttrCodeInput]; found {
		t.Errorf("the box is still open onto %v", of)
	}
}

// The same sentence, about somewhere else. An element of another input is not
// this box's own subtree, and there is another box that it is inside of — so
// the mapping is not about this one, and its answer about its own code stands.
func TestAnElementOfAnotherInputLeavesThisBoxAlone(t *testing.T) {
	const mine = "repo-1-a-json"
	g := graphRunning("registry.example/checkout:1.4.0")
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: mine, Path: "a.json", Kind: "graph"},
		{ID: mine + ":repo-2-svc", Path: "../svc", Kind: "repository"},
		{ID: "repo-2-b-json", Path: "b.json", Kind: "graph"},
	}}
	g.Nodes = append(g.Nodes,
		core.Node{ID: mine + ":repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": mine, "code_input": mine + ":repo-2-svc"}},
		core.Node{ID: "repo-2-b-json:repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-2-b-json"}},
		core.Node{ID: "repo-2-b-json:file:main.go", Type: "code_file", Name: "main.go",
			Attrs: map[string]any{"repository": "repo-2-b-json"}})
	g.Normalize()

	if _, err := (Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": "repo-2-b-json:file:main.go"},
	}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	n, _ := g.Node(mine + ":repository:acme/checkout")
	if of, _ := n.Attrs[AttrCodeInput].(string); of != mine+":repo-2-svc" {
		t.Errorf("this box's answer about its own code became %q", of)
	}
}

// Two boxes for one repository, the second made by this run for a workload the
// first does not cover. Both of them are that repository, and where that
// repository's code is does not change with which input a box came from — so
// both say it.
//
// Two tests here used to assert the opposite, that only one box ends up
// saying it. That was this package trying to stop the same code map being
// drawn twice, and it stopped the wrong thing: the box a fresh estate's
// workload points at is the one this run just made, so the reader who clicked
// their own container arrived at a box that opened nothing. Drawing it once is
// the atlas's business, and the atlas names a code map after the code it draws.
func TestEveryBoxForOneRepositorySaysWhereItsCodeIs(t *testing.T) {
	const code = "repo-3-checkout"
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: "repo-1-old-json", Path: "old.json", Kind: "graph"},
		{ID: "repo-2-fresh-yaml", Path: "fresh.yaml", Kind: "kubernetes"},
		{ID: code, Path: "../checkout", Kind: "repository"},
	}}
	g.Nodes = []core.Node{
		{ID: "repo-1-old-json:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{
				"image":      "registry.example/checkout:1.4.0",
				"repository": "repo-1-old-json",
			}},
		{ID: "repo-2-fresh-yaml:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{
				"image":      "registry.example/checkout:1.4.0",
				"repository": "repo-2-fresh-yaml",
			}},
		{ID: "repo-1-old-json:repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-old-json"}},
	}
	g.Normalize()

	if _, err := (Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": code},
	}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	boxes := repositoriesNamed(g, "acme/checkout")
	if len(boxes) != 2 {
		t.Fatalf("%d boxes for one repository", len(boxes))
	}
	for _, n := range boxes {
		if of, _ := n.Attrs[AttrCodeInput].(string); of != code {
			t.Errorf("%s opens %q", n.ID, of)
		}
	}

	// And the fresh estate's workload points at the box this run made for it.
	for _, e := range g.Edges {
		if e.Relation != Relation || e.From != "repo-2-fresh-yaml:workload:shop/checkout" {
			continue
		}
		if e.To != "repository:acme/checkout" {
			t.Errorf("the fresh workload was built from %s", e.To)
		}
	}
}

// The box the workload is inside of, already here and saying nothing about
// its code. The edge lands on it, so the reader who clicks their own container
// arrives there — and arrived at a box that opened nothing while this run had
// been told where the code is. Reusing a box is not a reason to keep that from
// it; replacing an answer is decided before any of this, and it has none.
func TestABoxAlreadyHereIsToldWhatThisRunWasTold(t *testing.T) {
	const code = "repo-2-checkout"
	g := core.New()
	g.Metadata = &core.Metadata{Inputs: []core.InputRef{
		{ID: "repo-1-a-json", Path: "a.json", Kind: "graph"},
		{ID: "repo-1-a-json:repo-9-old", Path: "../old", Kind: "repository"},
		{ID: code, Path: "../checkout", Kind: "repository"},
	}}
	g.Nodes = []core.Node{
		{ID: "repo-1-a-json:workload:shop/checkout", Type: "kubernetes_deployment", Name: "checkout",
			Attrs: map[string]any{
				"image":      "registry.example/checkout:1.4.0",
				"repository": "repo-1-a-json",
			}},
		// The mapping is about this one, so the one below is left alone.
		{ID: "repo-1-a-json:repo-9-old:repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-a-json:repo-9-old"}},
		// And this is the box the workload is inside of.
		{ID: "repo-1-a-json:repository:acme/checkout", Type: NodeRepository, Name: "acme/checkout",
			Attrs: map[string]any{"repository": "repo-1-a-json"}},
	}
	g.Normalize()

	if _, err := (Enricher{
		Documents:    []*builds.Document{record(t, oneBuild)},
		Repositories: map[string]string{"acme/checkout": code},
	}).Enrich(g); err != nil {
		t.Fatal(err)
	}

	to := ""
	for _, e := range g.Edges {
		if e.Relation == Relation {
			to = e.To
		}
	}
	if to != "repo-1-a-json:repository:acme/checkout" {
		t.Fatalf("the edge points at %s", to)
	}
	n, _ := g.Node(to)
	if of, _ := n.Attrs[AttrCodeInput].(string); of != code {
		t.Errorf("the box the workload points at opens %q", of)
	}
}
