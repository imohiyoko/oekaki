package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/schema"
)

const plan = "../../examples/three-tier/plan.json"

type result struct {
	code   int
	stdout string
	stderr string
}

func run(t *testing.T, stdin string, args ...string) result {
	t.Helper()

	var out, errBuf bytes.Buffer
	code := Run(context.Background(), Env{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errBuf,
	}, args)

	return result{code: code, stdout: out.String(), stderr: errBuf.String()}
}

func mustRun(t *testing.T, stdin string, args ...string) result {
	t.Helper()

	r := run(t, stdin, args...)
	if r.code != 0 {
		t.Fatalf("oekaki %s exited %d\nstderr: %s", strings.Join(args, " "), r.code, r.stderr)
	}
	return r
}

// The headline promise is that a clone runs immediately and produces a picture.
func TestRenderProducesSVG(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a.svg")
	mustRun(t, "", "render", plan, "-o", out)

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	svg := string(data)

	if !strings.Contains(svg, "<svg") {
		t.Fatal("output is not SVG")
	}
	// Graphviz escapes hyphens as &#45;, so labels are matched up to that point.
	for _, want := range []string{"ecs_service", "db_instance", "vpc: main", "subnet: public"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the diagram does not mention %q", want)
		}
	}
}

// Graphviz's WebAssembly build silently drops fill="none" when rasterising,
// so the SVG path is the one that has to keep edges as strokes.
func TestSVGEdgesAreStrokedNotFilled(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a.svg")
	mustRun(t, "", "render", plan, "-o", out)

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `<path fill="none" stroke=`) {
		t.Error("edge paths are not stroked, so the diagram will render as blobs")
	}
}

func TestFormatIsInferredFromTheExtension(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		file string
		want string
	}{
		{"a.svg", "<svg"},
		{"a.dot", "digraph oekaki"},
		{"a.mmd", "flowchart LR"},
		{"a.json", `"version": "0.5"`},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			out := filepath.Join(dir, tt.file)
			mustRun(t, "", "render", plan, "-o", out)

			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tt.want) {
				t.Errorf("%s does not contain %q", tt.file, tt.want)
			}
		})
	}
}

func TestExplicitFormatBeatsTheExtension(t *testing.T) {
	out := filepath.Join(t.TempDir(), "misleading.svg")
	mustRun(t, "", "render", plan, "-f", "dot", "-o", out)

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "digraph") {
		t.Error("-f was ignored in favour of the file extension")
	}
}

func TestLayoutIsOnlyAcceptedForHTML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.json")
	if err := os.WriteFile(path, []byte(`{"kind":"oekaki.layout","version":"0.1","nodes":[],"claim":{"origin":"human"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := run(t, "", "render", plan, "--layout", path, "-f", "dot")
	if r.code == 0 || !strings.Contains(r.stderr, "only supported with HTML") {
		t.Fatalf("layout was accepted for dot: %#v", r)
	}
}

// A layout that no longer fits its graph still applies — the browser places
// what it recognises. The risk is not that it fails; it is that it succeeds
// quietly while half the positions land nowhere.
func TestALayoutSaysHowMuchOfItLanded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.json")
	doc := `{"kind":"oekaki.layout","version":"0.1","nodes":[` +
		`{"id":"aws_ecs_service.api","x":1,"y":2},` +
		`{"id":"account:gone","x":3,"y":4}],"claim":{"origin":"human"}}`
	if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "page.html")
	r := mustRun(t, "", "render", plan, "--layout", path, "-f", "html", "-o", out)

	for _, want := range []string{"2 positions, 1 placed, 1 not in this graph", "not placed: account:gone"} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("layout report did not mention %q:\n%s", want, r.stderr)
		}
	}
}

// Without --layout there is nothing to report, and a line about a document
// nobody asked for is noise that trains people to ignore the channel.
func TestNothingIsSaidAboutALayoutThatWasNotGiven(t *testing.T) {
	out := filepath.Join(t.TempDir(), "page.html")
	r := mustRun(t, "", "render", plan, "-f", "html", "-o", out)

	if strings.Contains(r.stderr, "layout") {
		t.Fatalf("a run without --layout mentioned layout:\n%s", r.stderr)
	}
}

func TestLayoutIsEmbeddedByCLI(t *testing.T) {
	layout := filepath.Join(t.TempDir(), "layout.json")
	if err := os.WriteFile(layout, []byte(`{"kind":"oekaki.layout","version":"0.1","nodes":[{"id":"aws_ecs_service.api","x":12,"y":34}],"claim":{"origin":"human"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, "", "render", plan, "-f", "html", "--layout", layout).stdout
	if !strings.Contains(out, `id="oekaki-layout"`) || !strings.Contains(out, `"x":12`) {
		t.Fatal("CLI did not embed layout")
	}
}

func TestHTMLReceivesRankDirectionAndKindFilter(t *testing.T) {
	out := mustRun(t, "", "render", plan, "-f", "html", "--rankdir", "TB", "--kind", "observed").stdout
	if !strings.Contains(out, `data-rankdir="TB"`) || !strings.Contains(out, `data-kinds="observed"`) {
		t.Fatal("HTML renderer ignored CLI layout/filter options")
	}
}

// Go's flag package stops at the first positional argument, so flags written
// after the input file would otherwise be silently ignored.
func TestFlagsAfterThePositionalArgumentStillApply(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a.dot")
	mustRun(t, "", "render", plan, "-f", "dot", "-o", out, "--title", "placed last")

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `label="placed last"`) {
		t.Error("a flag written after the input file was ignored")
	}
}

func TestGraphThenRenderRoundTrips(t *testing.T) {
	graph := mustRun(t, "", "graph", plan).stdout

	// Feeding the IR back in must produce the same picture as the plan did,
	// which is what makes committing a graph and re-rendering it worthwhile.
	fromGraph := mustRun(t, graph, "render", "-", "-f", "dot").stdout
	fromPlan := mustRun(t, "", "render", plan, "-f", "dot").stdout

	if fromGraph != fromPlan {
		t.Error("rendering the IR gave a different result than rendering the plan")
	}
}

func TestGraphOutputValidates(t *testing.T) {
	graph := mustRun(t, "", "graph", plan).stdout

	r := mustRun(t, graph, "validate", "-")
	if !strings.HasPrefix(r.stdout, "ok:") {
		t.Errorf("validate said %q", r.stdout)
	}
}

func TestMultipleRepositoriesAreNamespacedAndCombined(t *testing.T) {
	root := t.TempDir()
	repoA := filepath.Join(root, "checkout")
	repoB := filepath.Join(root, "billing")
	if err := os.MkdirAll(repoA, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoA, "main.py"), []byte("def handle():\n  pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoB, "main.py"), []byte("def handle():\n  pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, "", "graph", "--repo", repoA, "--repo", repoB).stdout
	if !strings.Contains(out, `"id": "repo-1-checkout:file:main.py#handle"`) || !strings.Contains(out, `"id": "repo-2-billing:file:main.py#handle"`) {
		t.Fatalf("repository namespaces were not present: %s", out)
	}
	if !strings.Contains(out, `"inputs"`) || !strings.Contains(out, `"repository": "repo-1-checkout"`) {
		t.Fatalf("combined graph did not expose input context: %s", out)
	}
	r := mustRun(t, out, "validate", "-")
	if !strings.Contains(r.stdout, "ok:") {
		t.Fatalf("combined graph did not validate: %s", r.stdout)
	}
}

func TestMultipleGraphInputsMergeSharedExternalNodesAndProvenance(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 0, 2)
	for i, id := range []string{"service:a", "service:b"} {
		g := core.New()
		g.Metadata = &core.Metadata{
			Source:        "graph",
			SourceVersion: []string{"revision-a", "revision-b"}[i],
			Inputs: []core.InputRef{{
				ID:            "nested",
				Path:          "source.json",
				Kind:          "graph",
				SourceVersion: []string{"nested-revision-a", "nested-revision-b"}[i],
			}},
			Overlays: []core.OverlayRef{{Source: "overlay-" + id + ".json", Origin: core.OriginHuman}},
		}
		g.Nodes = []core.Node{
			{ID: id, Type: "service", Name: id},
			{ID: "external:internet", Type: "external_endpoint", Name: "Internet", Provider: "external"},
		}
		g.Edges = []core.Edge{{From: id, To: "external:internet", Kind: core.EdgeReachable, Relation: "reachable"}}
		g.LogStatus = &core.LogCollectionStatus{
			StartedAt: "2026-08-29T0" + string(rune('1'+i)) + ":00:00Z", CompletedAt: "2026-08-29T0" + string(rune('2'+i)) + ":00:00Z",
			Fetched: i + 1, Classified: i + 1,
		}
		if i == 1 {
			g.LogStatus.LastError = "backend unavailable"
		}
		g.Normalize()
		raw, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "graph-"+string(rune('a'+i))+".json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	out := mustRun(t, "", "graph", "--repo", paths[0], "--repo", paths[1]).stdout
	g, err := core.Decode(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	var external int
	for _, node := range g.Nodes {
		if node.ID == "external:internet" {
			external++
			if _, ok := node.Attrs["repository"]; ok {
				t.Fatal("shared external node was assigned to one repository")
			}
		}
	}
	if external != 1 {
		t.Fatalf("external node count = %d, want 1", external)
	}
	if len(g.Metadata.Overlays) != 2 || len(g.Metadata.Inputs) != 4 {
		t.Fatalf("combined provenance = %#v", g.Metadata)
	}
	versions := make(map[string]string, len(g.Metadata.Inputs))
	for _, input := range g.Metadata.Inputs {
		versions[input.ID] = input.SourceVersion
	}
	for i, path := range paths {
		scope := repositoryScope(path, i)
		if got, want := versions[scope], []string{"revision-a", "revision-b"}[i]; got != want {
			t.Fatalf("combined input %q source version = %q, want %q", scope, got, want)
		}
		if got, want := versions[scope+":nested"], []string{"nested-revision-a", "nested-revision-b"}[i]; got != want {
			t.Fatalf("nested input %q source version = %q, want %q", scope, got, want)
		}
	}
	if g.LogStatus == nil || g.LogStatus.Fetched != 3 || g.LogStatus.Classified != 3 || !strings.Contains(g.LogStatus.LastError, "repo-2-") {
		t.Fatalf("combined log status = %#v", g.LogStatus)
	}
}

func TestMergeLogStatusUsesTimestampOrder(t *testing.T) {
	dst := &core.LogCollectionStatus{
		StartedAt:   "2026-08-29T00:00:00.1Z",
		CompletedAt: "2026-08-29T00:00:00Z",
	}
	src := &core.LogCollectionStatus{
		StartedAt:   "2026-08-29T00:00:00Z",
		CompletedAt: "2026-08-29T00:00:00.1Z",
	}

	got := mergeLogStatus(dst, src, "repo")
	if got.StartedAt != src.StartedAt || got.CompletedAt != src.CompletedAt {
		t.Fatalf("merged timestamp range = %#v", got)
	}
}

func TestValidateAcceptsObservationDocument(t *testing.T) {
	r := mustRun(t, `{"kind":"oekaki.observations","version":"1","observations":[{"subject":"service:checkout","metric":"error_rate","labels":{"sensor":"api"},"value":0.2}]}`, "validate", "-")
	if !strings.Contains(r.stdout, "observations document") {
		t.Fatalf("unexpected validation output: %q", r.stdout)
	}
}

func TestProbeEmitsNormalizedReachabilityDocument(t *testing.T) {
	out := mustRun(t, "", "probe", plan, "--from", "aws_ecs_service.api", "--target", "aws_rds_instance.db=127.0.0.1:1", "--timeout", "10ms").stdout
	var doc struct {
		Kind  string `json:"kind"`
		Paths []struct {
			From    string `json:"from"`
			To      string `json:"to"`
			Allowed bool   `json:"allowed"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "oekaki.reachability" || len(doc.Paths) != 1 || doc.Paths[0].From != "aws_ecs_service.api" || doc.Paths[0].Allowed {
		t.Fatalf("unexpected probe output: %#v", doc)
	}
}

func TestValidateRejectsABrokenGraph(t *testing.T) {
	const broken = `{"version":"0.5","axes":[],"nodes":[],"edges":[{"from":"a","to":"b","kind":"iac_ref"}],"groups":[]}`

	r := run(t, broken, "validate", "-")
	if r.code == 0 {
		t.Fatal("a graph with a dangling edge was accepted")
	}
	if !strings.Contains(r.stderr, "unknown source") {
		t.Errorf("stderr does not explain the problem: %q", r.stderr)
	}
}

func TestGraphIsDeterministicAcrossRuns(t *testing.T) {
	first := mustRun(t, "", "graph", plan).stdout
	for range 5 {
		if got := mustRun(t, "", "graph", plan).stdout; got != first {
			t.Fatal("two runs produced different graphs")
		}
	}
}

func TestSourceDirAddsLocations(t *testing.T) {
	out := mustRun(t, "", "graph", plan, "--source-dir", "../../examples/three-tier").stdout

	var g struct {
		Nodes []struct {
			ID     string `json:"id"`
			Source *struct {
				File string `json:"file"`
				Line int    `json:"line"`
			} `json:"source"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}

	for _, n := range g.Nodes {
		if n.Source == nil || n.Source.File == "" || n.Source.Line == 0 {
			t.Errorf("%s has no usable source location", n.ID)
		}
	}
}

func TestKindFilterIsValidated(t *testing.T) {
	r := run(t, "", "render", plan, "-f", "dot", "--kind", "made_up")
	if r.code == 0 {
		t.Fatal("an unknown edge kind was accepted")
	}
	if !strings.Contains(r.stderr, "iac_ref") {
		t.Errorf("the error does not list the valid kinds: %q", r.stderr)
	}
}

func TestSchemaCommandPrintsTheContract(t *testing.T) {
	out := mustRun(t, "", "schema").stdout

	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the printed schema is not valid JSON: %v", err)
	}
	if doc["$schema"] == nil {
		t.Error("the printed document is not a JSON Schema")
	}
}

func TestAICommandAddsValidatedOpaqueNode(t *testing.T) {
	d := t.TempDir()
	command := filepath.Join(d, "model-adapter")
	aiArgs := []string{"ignored"}
	var script []byte
	if runtime.GOOS == "windows" {
		command += ".bat"
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		aiArgs = []string{"/d", "/c", command}
		command = comspec
		script = []byte("@echo off\r\necho {\"kind\":\"oekaki.ai-candidates\",\"version\":\"1\",\"nodes\":[{\"id\":\"library:opaque\",\"type\":\"code_package\",\"name\":\"opaque-client\"}]}\r\n")
	} else {
		script = []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"kind\":\"oekaki.ai-candidates\",\"version\":\"1\",\"nodes\":[{\"id\":\"library:opaque\",\"type\":\"code_package\",\"name\":\"opaque-client\"}]}'\n")
	}
	scriptPath := command
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(d, "model-adapter.bat")
	}
	if err := os.WriteFile(scriptPath, script, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"graph", plan, "--ai-command", command}
	for _, arg := range aiArgs {
		args = append(args, "--ai-arg", arg)
	}
	out := mustRun(t, "", args...).stdout
	if !strings.Contains(out, `"id": "library:opaque"`) || !strings.Contains(out, `"origin": "ai"`) {
		t.Fatalf("AI command output did not include validated node: %s", out)
	}
}

// `go install module@version` applies no ldflags, so a release installed the
// way the README recommends has to get its version from the build info
// instead of the stamp.
func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name      string
		stamped   string
		fromBuild string
		want      string
	}{
		{"goreleaser stamp wins", "0.1.0", "v0.1.0", "0.1.0"},
		{"stamp wins even over a different build version", "0.2.0", "v0.1.0", "0.2.0"},
		{"go install: fall back to the module version", "dev", "v0.1.0", "v0.1.0"},
		{"go build from a working tree", "dev", "(devel)", "dev"},
		{"no build info at all", "dev", "", "dev"},
		{"empty stamp behaves like dev", "", "v0.1.0", "v0.1.0"},
		{"empty stamp and no build info", "", "", "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.stamped, tt.fromBuild); got != tt.want {
				t.Errorf("resolveVersion(%q, %q) = %q, want %q", tt.stamped, tt.fromBuild, got, tt.want)
			}
		})
	}
}

// The version also lands in every graph's metadata, so the two must not be
// allowed to disagree.
func TestGraphMetadataCarriesTheSameVersion(t *testing.T) {
	out := mustRun(t, "", "graph", plan).stdout

	var g struct {
		Metadata struct {
			Generator string `json:"generator"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}

	if want := "oekaki/" + version(); g.Metadata.Generator != want {
		t.Errorf("generator = %q, want %q", g.Metadata.Generator, want)
	}
}

func TestVersionAndHelp(t *testing.T) {
	if out := mustRun(t, "", "version").stdout; !strings.Contains(out, "oekaki") {
		t.Errorf("version printed %q", out)
	}
	if out := mustRun(t, "", "help").stdout; !strings.Contains(out, "render") {
		t.Error("help does not mention the render command")
	}
}

// Manifests reach the same renderers Terraform output does. Wiring the parser
// into loadGraph is what makes `helm template | oekaki render -` work at all,
// and a parser nothing calls is a parser that does not exist.
func TestManifestsRenderThroughTheSameCommands(t *testing.T) {
	manifests := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: checkout
  namespace: shop
spec:
  template:
    metadata:
      labels:
        app: checkout
    spec:
      containers:
      - name: checkout
        image: registry.example/checkout:1.4.0
---
apiVersion: v1
kind: Service
metadata:
  name: checkout
  namespace: shop
spec:
  selector:
    app: checkout
`

	r := run(t, manifests, "graph", "-")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if err := schema.Validate([]byte(r.stdout)); err != nil {
		t.Fatalf("the emitted graph does not match the schema: %v", err)
	}
	for _, want := range []string{"deployment/shop/checkout", "service/shop/checkout", "selects"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the graph does not mention %q", want)
		}
	}
	// The reader has to be told which cluster releases will accept this.
	if !strings.Contains(r.stderr, "kubernetes: 2 objects") {
		t.Errorf("stderr %q does not report what was read", r.stderr)
	}
}

func TestErrorsAreReportedUsefully(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		stdin   string
		wantSub string
	}{
		{"no arguments", nil, "", "Usage"},
		{"unknown command", []string{"draw"}, "", "unknown command"},
		{"missing file", []string{"render", "nope.json"}, "", "nope.json"},
		{"no input file", []string{"render"}, "", "exactly one input file"},
		{"unknown format", []string{"render", plan, "-f", "pdf"}, "", "unknown format"},
		{"undetectable format", []string{"render", plan, "-o", "out.txt"}, "", "cannot tell the format"},
		{"not terraform output", []string{"render", "-"}, `{"hello":"world"}`, "is not `terraform show -json` output, an oekaki graph, or Kubernetes manifests"},
		{"not json at all", []string{"render", "-"}, `hello`, "is not `terraform show -json` output, an oekaki graph, or Kubernetes manifests"},
		{"yaml that is not manifests", []string{"render", "-"}, "just: a map\n", "no apiVersion or kind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := run(t, tt.stdin, tt.args...)
			if r.code == 0 {
				t.Fatal("expected a non-zero exit code")
			}
			if !strings.Contains(r.stderr, tt.wantSub) {
				t.Errorf("stderr %q does not mention %q", r.stderr, tt.wantSub)
			}
		})
	}
}

func TestOutputDirectoryIsCreated(t *testing.T) {
	out := filepath.Join(t.TempDir(), "nested", "deeper", "a.dot")
	mustRun(t, "", "render", plan, "-o", out)

	if _, err := os.Stat(out); err != nil {
		t.Fatalf("the output directory was not created: %v", err)
	}
}

func TestFencedMermaidIsPasteable(t *testing.T) {
	out := mustRun(t, "", "render", plan, "-f", "mermaid", "--fenced").stdout

	if !strings.HasPrefix(out, "```mermaid") {
		t.Error("--fenced did not wrap the output in a code fence")
	}
}

func TestQualifyGraphPreservesRelationInConflictTargets(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "a", Type: "service"}, {ID: "b", Type: "service"}}
	g.Edges = []core.Edge{{From: "a", To: "b", Kind: core.EdgeIACRef, Relation: "calls"}}
	g.Conflicts = []core.Conflict{{TargetKind: core.ConflictTargetEdge, Target: core.EdgeKey("a", "b", core.EdgeIACRef, "calls"), Field: "claim", Claims: []core.ClaimedValue{{Value: "one", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "two", Claim: core.Claim{Origin: core.OriginParser}}}}}
	qualifyGraph(g, "repo-1")
	if got, want := g.Conflicts[0].Target, core.EdgeKey("repo-1:a", "repo-1:b", core.EdgeIACRef, "calls"); got != want {
		t.Fatalf("conflict target = %q, want %q", got, want)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("qualified graph should validate: %v", err)
	}
}

func TestQualifyGraphHandlesSeparatorsInTypedConflictTargets(t *testing.T) {
	t.Run("edge endpoints and relation", func(t *testing.T) {
		g := core.New()
		g.Nodes = []core.Node{{ID: "a|left", Type: "service"}, {ID: "b|right", Type: "service"}}
		g.Edges = []core.Edge{{From: "a|left", To: "b|right", Kind: core.EdgeObserved, Relation: "calls|reads"}}
		g.Conflicts = []core.Conflict{{
			TargetKind: core.ConflictTargetEdge,
			Target:     core.EdgeKey("a|left", "b|right", core.EdgeObserved, "calls|reads"),
			Field:      "suppressed",
			Claims:     []core.ClaimedValue{{Value: "false", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "true", Claim: core.Claim{Origin: core.OriginHuman}}},
		}}

		qualifyGraph(g, "repo-1")
		if got, want := g.Conflicts[0].Target, core.EdgeKey("repo-1:a|left", "repo-1:b|right", core.EdgeObserved, "calls|reads"); got != want {
			t.Fatalf("qualified edge conflict target = %q, want %q", got, want)
		}
		if err := g.Validate(); err != nil {
			t.Fatalf("qualified graph should validate: %v", err)
		}
	})

	t.Run("entity that resembles an edge key", func(t *testing.T) {
		id := core.EdgeKey("a", "b", core.EdgeIACRef)
		g := core.New()
		g.Nodes = []core.Node{{ID: id, Type: "service"}}
		g.Conflicts = []core.Conflict{{
			TargetKind: core.ConflictTargetEntity,
			Target:     id,
			Field:      "name",
			Claims:     []core.ClaimedValue{{Value: "one", Claim: core.Claim{Origin: core.OriginParser}}, {Value: "two", Claim: core.Claim{Origin: core.OriginHuman}}},
		}}

		qualifyGraph(g, "repo-1")
		if got, want := g.Conflicts[0].Target, "repo-1:"+id; got != want {
			t.Fatalf("qualified entity conflict target = %q, want %q", got, want)
		}
		if err := g.Validate(); err != nil {
			t.Fatalf("qualified graph should validate: %v", err)
		}
	})
}

func TestQualifyGraphQualifiesInlineSecurityGroupReferences(t *testing.T) {
	g := core.New()
	g.Nodes = []core.Node{{ID: "aws_security_group.alb", Type: "aws_security_group", Attrs: map[string]any{
		"ingress": []any{map[string]any{"security_groups": []any{"aws_security_group.alb.id"}}},
	}}}
	qualifyGraph(g, "repo-1")
	ingress := g.Nodes[0].Attrs["ingress"].([]any)
	rule := ingress[0].(map[string]any)
	if got, want := rule["security_groups"].([]any)[0], "repo-1:aws_security_group.alb.id"; got != want {
		t.Fatalf("inline security group reference = %q, want %q", got, want)
	}
}

// The flag exists to produce a directory a server can hand out: the page, the
// graph it fetches, and one copy of the runtime every other page can share.
func TestExternalAssetsWriteTheRuntimeBesideThePage(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "a.html")
	mustRun(t, "", "render", plan, "-f", "html", "--external-assets", "-o", out)

	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) > 50_000 {
		t.Errorf("the page is %d bytes, so it is still carrying the runtime", len(page))
	}
	for _, name := range []string{"a.graph.json", "oekaki.elk.js", "oekaki.app.js", "oekaki.app.css", "oekaki.boot.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
}

// The document the page fetches has to be the one the same command would have
// written as JSON. A viewer showing something that exists in no file is the
// failure this project exists to avoid.
func TestTheFetchedGraphIsTheDocumentTheCommandWouldHaveWritten(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, "", "render", plan, "-f", "html", "--external-assets", "-o", filepath.Join(dir, "a.html"))

	fetched, err := os.ReadFile(filepath.Join(dir, "a.graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := mustRun(t, "", "render", plan, "-f", "json").stdout; string(fetched) != want {
		t.Error("the fetched graph is not the document -f json produces")
	}
}

func TestExternalAssetsWithoutAnOutputFileIsRejected(t *testing.T) {
	r := run(t, "", "render", plan, "-f", "html", "--external-assets")

	if r.code == 0 {
		t.Fatal("a page whose siblings had nowhere to go was accepted")
	}
	if !strings.Contains(r.stderr, "-o") {
		t.Errorf("the error does not say what to do: %s", r.stderr)
	}
}

// A base with a scheme describes what a server exposes, not a directory this
// command can reach. Writing there would guess at somebody else's layout.
func TestAURLAssetBaseWritesNoRuntime(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, "", "render", plan, "-f", "html", "--external-assets",
		"--asset-base", "https://cdn.example/v1", "-o", filepath.Join(dir, "a.html"))

	if _, err := os.Stat(filepath.Join(dir, "oekaki.elk.js")); err == nil {
		t.Error("the runtime was written beside a page that loads it from a url")
	}
	// The graph is still fetched relative to the page, so it is still written.
	if _, err := os.Stat(filepath.Join(dir, "a.graph.json")); err != nil {
		t.Errorf("the graph document was not written: %v", err)
	}
}

// The point of scan is that it needs neither an initialised working directory
// nor credentials: what is committed is enough.
func TestScanBuildsAGraphFromCommittedSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "terraform {\n  backend \"s3\" {\n    key = \"states/app\"\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "app", "provider.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	r := mustRun(t, "", "scan", dir)

	if !strings.Contains(r.stdout, `"id": "module:states/app"`) {
		t.Fatalf("the module is not in the graph:\n%s", r.stdout)
	}
	// An empty scan and an empty estate look the same in the output, so the
	// count is the only thing that tells them apart.
	if !strings.Contains(r.stderr, "1 root modules") {
		t.Fatalf("nothing said what was found:\n%s", r.stderr)
	}
}

// An overlay can be made to fail the build when it names nothing in the graph
// and a layout could not, which meant the two halves of the same idea had
// different teeth. Drift that is only ever printed is drift nobody acts on.
func TestALayoutNamingNothingCanBeMadeToFail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.layout.json")
	doc := `{"kind":"oekaki.layout","version":"0.2","nodes":[` +
		`{"id":"gone.away","x":1,"y":2}],"claim":{"origin":"human"}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "page.html")

	// The default says so and carries on, because a stale position is a worse
	// picture and no picture is worse than that.
	got := mustRun(t, "", "render", plan, "--layout", path, "-o", out)
	if !strings.Contains(got.stderr, "not placed: gone.away") {
		t.Fatalf("the drift was not named: %q", got.stderr)
	}

	// Asked to, it refuses.
	strict := run(t, "", "render", plan, "--layout", path, "--layout-unmatched", "error", "-o", out)
	if strict.code == 0 {
		t.Fatalf("it carried on: %q", strict.stderr)
	}
	if !strings.Contains(strict.stderr, "name nothing in this graph") {
		t.Fatalf("%q", strict.stderr)
	}
}

// Adopt is an overlay's answer, because an assertion naming nothing can become
// a node. A position is not a statement that something exists, so there is
// nothing for it to be adopted into.
func TestALayoutHasNoAdoptToOffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.layout.json")
	doc := `{"kind":"oekaki.layout","version":"0.2","nodes":[],"claim":{"origin":"human"}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	got := run(t, "", "render", plan, "--layout", path, "--layout-unmatched", "adopt",
		"-o", filepath.Join(dir, "page.html"))
	if got.code == 0 {
		t.Fatal("adopt was accepted")
	}
	if !strings.Contains(got.stderr, "report or error") {
		t.Fatalf("%q", got.stderr)
	}
}

// A directory named after the bytes in it can be shared by every page of every
// generation and never has to be invalidated. Overwriting a fixed directory
// hands a fresh runtime to pages drawn against an older one, and the query
// fingerprint cannot help with that: it changes what the browser caches, not
// what the server has on disk.
func TestTheRuntimeCanBeWrittenToADirectoryNamedAfterItsBytes(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "runs", "r1", "core.html")
	mustRun(t, "", "render", plan, "-f", "html", "--external-assets",
		"--asset-base", "../../shell/auto", "-o", page)

	entries, err := os.ReadDir(filepath.Join(dir, "shell"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one fingerprinted directory: %#v", entries)
	}
	stamp := entries[0].Name()
	if stamp == "auto" {
		t.Fatal("the word was taken literally")
	}

	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	// The page has to point at the directory that was written, and the query
	// fingerprint has to agree with it.
	if !strings.Contains(string(body), "../../shell/"+stamp+"/") {
		t.Fatalf("the page does not name the directory %q", stamp)
	}
	if !strings.Contains(string(body), "?v="+stamp) {
		t.Fatalf("the two fingerprints disagree: %q", stamp)
	}
}

// Rendering twice from one build has to land in the same place, or every run
// leaves another copy of an identical runtime behind.
func TestTheSameRuntimeLandsInTheSameDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		mustRun(t, "", "render", plan, "-f", "html", "--external-assets",
			"--asset-base", "../../shell/auto",
			"-o", filepath.Join(dir, "runs", name, "core.html"))
	}
	entries, err := os.ReadDir(filepath.Join(dir, "shell"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("two runs of one build left %d directories: %#v", len(entries), entries)
	}
}

// A base that says nothing about fingerprints has to keep working exactly as
// it did.
func TestAPlainAssetBaseIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "core.html")
	mustRun(t, "", "render", plan, "-f", "html", "--external-assets",
		"--asset-base", "shell/v1", "-o", page)
	if _, err := os.Stat(filepath.Join(dir, "shell", "v1", "oekaki.app.js")); err != nil {
		t.Fatalf("the runtime did not go where it was asked: %v", err)
	}
}

// A misspelt policy has to be a message rather than a flag that quietly did
// nothing on the run where it happened not to matter.
func TestAMisspeltLayoutPolicyIsCaughtWithoutALayoutToApplyIt(t *testing.T) {
	got := run(t, "", "render", plan, "--layout-unmatched", "adopt",
		"-o", filepath.Join(t.TempDir(), "page.html"))
	if got.code == 0 {
		t.Fatalf("it was accepted when no layout was given: %q", got.stderr)
	}
	if !strings.Contains(got.stderr, "report or error") {
		t.Fatalf("%q", got.stderr)
	}
}

// DOT and Mermaid are handed to something else to draw, so a stylesheet given
// with them would be read, accepted and thrown away. Saying so beats a picture
// that comes back unstyled with no explanation of why.
func TestAStylesheetIsOnlyAcceptedWhereItCanGo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.css")
	if err := os.WriteFile(path, []byte(".edge { stroke: red; }"), 0600); err != nil {
		t.Fatal(err)
	}
	r := run(t, "", "render", plan, "--css", path, "-f", "mermaid")
	if r.code == 0 || !strings.Contains(r.stderr, "only html and svg") {
		t.Fatalf("a stylesheet was accepted for mermaid: %#v", r)
	}
}

func TestAStylesheetReachesBothPictureFormats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "theme.css")
	if err := os.WriteFile(path, []byte(".edge { stroke: rebeccapurple; }"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"html", "svg"} {
		r := mustRun(t, "", "render", plan, "--css", path, "-f", format)
		if !strings.Contains(r.stdout, "rebeccapurple") {
			t.Errorf("%s output does not carry the stylesheet it was given", format)
		}
	}
}

// The runtime directory is named after the bytes in it so that pages rendered
// against one runtime go on reading it. The theme is served out of that
// directory too, so editing the theme has to land in a different one — or the
// old stylesheet stays where every page is still looking.
func TestTheSharedRuntimeMovesWhenTheThemeDoes(t *testing.T) {
	dir := t.TempDir()
	shells := map[string]bool{}
	for _, colour := range []string{"red", "blue"} {
		theme := filepath.Join(dir, colour+".css")
		if err := os.WriteFile(theme, []byte(".edge { stroke: "+colour+"; }"), 0600); err != nil {
			t.Fatal(err)
		}
		page := filepath.Join(dir, colour, "page.html")
		mustRun(t, "", "render", plan, "--css", theme, "-f", "html",
			"--external-assets", "--asset-base", "shell/auto", "-o", page)

		found, err := filepath.Glob(filepath.Join(dir, colour, "shell", "*", "oekaki.app.css"))
		if err != nil || len(found) != 1 {
			t.Fatalf("looking for the shared stylesheet: %v %v", found, err)
		}
		css, err := os.ReadFile(found[0])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(css), "stroke: "+colour) {
			t.Errorf("the shared stylesheet does not carry the %s theme", colour)
		}
		shells[filepath.Base(filepath.Dir(found[0]))] = true
	}
	if len(shells) != 2 {
		t.Error("both themes were written to the same directory, so the second is served the first")
	}
}

// inputKind is the only place a downstream tool learns what a combined graph
// was built from. A parser added without a case here reports its input as an
// oekaki graph, which says the context was already derived when it was not.
func TestInputKindNamesEveryParser(t *testing.T) {
	for source, want := range map[string]string{
		"terraform":  "terraform",
		"source":     "repository",
		"kubernetes": "kubernetes",
		"":           "graph",
	} {
		g := core.New()
		g.Metadata = &core.Metadata{Source: source}
		if got := inputKind(g); got != want {
			t.Errorf("inputKind(%q) = %q, want %q", source, got, want)
		}
	}
}
