package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imohiyoko/oekaki/core"
)

// An estate a request enters at the gateway, and the traces of what actually
// walked it: one route in full, one that stopped halfway, and one that visited
// the same services in an order nothing declares.
func tracedEstate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	estate := filepath.Join(dir, "estate.json")
	traces := filepath.Join(dir, "traces.json")
	out := filepath.Join(dir, "traced.json")

	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(estate, `{"version":"0.5","axes":[],"groups":[],
		"nodes":[
			{"id":"gateway","type":"service","name":"gateway"},
			{"id":"checkout","type":"service","name":"checkout"},
			{"id":"ledger","type":"service","name":"ledger"},
			{"id":"reports","type":"service","name":"reports"},
			{"id":"archive","type":"service","name":"archive"}],
		"edges":[
			{"from":"gateway","to":"checkout","kind":"iac_ref","relation":"calls"},
			{"from":"checkout","to":"ledger","kind":"iac_ref","relation":"calls"},
			{"from":"gateway","to":"reports","kind":"iac_ref","relation":"calls"},
			{"from":"reports","to":"archive","kind":"iac_ref","relation":"calls"}]}`)
	write(traces, `{"version":"1","spans":[
		{"trace_id":"t1","service":"gateway"},
		{"trace_id":"t1","service":"checkout","parent_service":"gateway"},
		{"trace_id":"t1","service":"ledger","parent_service":"checkout","observed_at":"2026-09-02T10:00:00Z"},
		{"trace_id":"t2","service":"gateway"},
		{"trace_id":"t2","service":"ledger","parent_service":"gateway","observed_at":"2026-09-03T02:13:00Z"},
		{"trace_id":"t3","service":"gateway"},
		{"trace_id":"t3","service":"reports","parent_service":"gateway","observed_at":"2026-05-01T10:00:00Z"}]}`)

	mustRun(t, "", "graph", estate, "--traces", traces, "-o", out)
	return out
}

// The listing an operator reads: what fired unannounced, what stopped halfway,
// and what nothing has ever walked.
func TestPathsListsWhatTheRoutesSayAboutEachOther(t *testing.T) {
	r := mustRun(t, "", "paths", tracedEstate(t))
	for _, want := range []string{
		"unexpected  gateway → ledger",
		"partial     gateway → reports → archive",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the listing does not report %q: %s", want, r.stdout)
		}
	}
	// A route walked in full is not a finding.
	if strings.Contains(r.stdout, "gateway → checkout → ledger") {
		t.Errorf("a route walked in full was reported: %s", r.stdout)
	}
	// The declared side was derived, and the listing says so rather than
	// presenting it as something somebody wrote down.
	if !strings.Contains(r.stderr, "derived by following references") {
		t.Errorf("the listing does not say where the declared routes came from: %s", r.stderr)
	}
}

func TestPathsWritesJSONWithTheWindowItAsked(t *testing.T) {
	r := mustRun(t, "", "paths", tracedEstate(t), "-f", "json", "--since", "2026-08-01T00:00:00Z")
	var doc struct {
		Since    string `json:"since"`
		Findings []struct {
			Kind string `json:"kind"`
			Key  string `json:"key"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	if doc.Since != "2026-08-01T00:00:00Z" {
		t.Errorf("the document does not carry the moment it was asking about: %q", doc.Since)
	}
	if len(doc.Findings) == 0 {
		t.Fatal("no findings")
	}
	for _, f := range doc.Findings {
		if !strings.HasPrefix(f.Key, "path:") {
			t.Errorf("a finding does not name its route: %#v", f)
		}
	}
}

func TestPathsFiltersToOneKind(t *testing.T) {
	graph := tracedEstate(t)
	r := mustRun(t, "", "paths", graph, "--only", "unexpected")
	if strings.Contains(r.stdout, "partial") {
		t.Errorf("--only unexpected listed something else: %s", r.stdout)
	}
	if !strings.Contains(r.stdout, "unexpected") {
		t.Errorf("--only unexpected listed nothing: %s", r.stdout)
	}
	if bad := run(t, "", "paths", graph, "--only", "loud"); bad.code == 0 {
		t.Error("an unknown finding kind was accepted")
	}
}

// A span is resolved against a clock, and the clock belongs to the caller —
// which is why the view never reads one and this function takes it.
func TestSinceAcceptsATimeOrASpan(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
	tests := map[string]string{
		"":                     "",
		"2026-08-01T00:00:00Z": "2026-08-01T00:00:00Z",
		"30d":                  "2026-08-07T12:00:00Z",
		"12h":                  "2026-09-06T00:00:00Z",
		"90m":                  "2026-09-06T10:30:00Z",
	}
	for in, want := range tests {
		got, err := resolveSince(in, now)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q became %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"soon", "30", "0d", "-5d", "yesterday", "3xd", "1 d"} {
		if got, err := resolveSince(bad, now); err == nil {
			t.Errorf("%q was accepted as %q", bad, got)
		}
	}
}

// Two repositories, each carrying routes of its own. The combined document has
// to keep both, with every participant and every reading renamed into its
// repository — a route is a list of ids, and a reading about one names it by
// an encoding of those ids.
func TestRoutesSurviveCombiningTwoRepositories(t *testing.T) {
	dir := t.TempDir()
	var inputs []string
	for i, service := range []string{"ledger", "search"} {
		g := core.New()
		g.Nodes = []core.Node{
			{ID: "gateway", Type: "service", Name: "gateway"},
			{ID: service, Type: "service", Name: service},
		}
		g.Edges = []core.Edge{{From: "gateway", To: service, Kind: core.EdgeObserved, Relation: "calls"}}
		g.Paths = []core.Path{{Nodes: []string{"gateway", service}, Kind: core.EdgeObserved}}
		count := float64(i + 1)
		g.Observations = []core.Observation{{
			Subject: core.PathKey([]string{"gateway", service}), Metric: "path_requests", Value: &count,
		}}
		g.Normalize()
		raw, err := g.MarshalIndent()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, service+".json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
	}

	out := mustRun(t, "", "graph", "--repo", inputs[0], "--repo", inputs[1]).stdout
	g, err := core.Decode(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Paths) != 2 {
		t.Fatalf("got %d routes, want both repositories' own: %#v", len(g.Paths), g.Paths)
	}
	for _, p := range g.Paths {
		for _, id := range p.Nodes {
			if !strings.Contains(id, ":") {
				t.Fatalf("a participant was not renamed into its repository: %q", id)
			}
			if _, ok := g.Node(id); !ok {
				t.Fatalf("the route walks %q, which the combined document does not have", id)
			}
		}
	}
	readings := 0
	for _, o := range g.Observations {
		nodes, isPath := core.ParsePathKey(o.Subject)
		if !isPath {
			continue
		}
		readings++
		if _, ok := g.Path(nodes, core.EdgeObserved); !ok {
			t.Fatalf("a reading is about %v, which is not a route this document carries", nodes)
		}
	}
	if readings != 2 {
		t.Fatalf("got %d readings about routes, want 2", readings)
	}
}
