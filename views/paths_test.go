package views

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// An estate where a request enters at the gateway and can go two ways.
func routes() *core.Graph {
	g := core.New()
	g.Nodes = []core.Node{
		{ID: "gateway", Type: "service", Name: "gateway"},
		{ID: "checkout", Type: "service", Name: "checkout"},
		{ID: "ledger", Type: "service", Name: "ledger"},
		{ID: "reports", Type: "service", Name: "reports"},
		{ID: "archive", Type: "service", Name: "archive"},
	}
	g.Edges = []core.Edge{
		{From: "gateway", To: "checkout", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "checkout", To: "ledger", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "gateway", To: "reports", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "reports", To: "archive", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Normalize()
	return g
}

func walked(g *core.Graph, at string, count float64, nodes ...string) {
	g.Paths = append(g.Paths, core.Path{Nodes: nodes, Kind: core.EdgeObserved})
	value := count
	g.Observations = append(g.Observations, core.Observation{
		Subject: core.PathKey(nodes), Metric: DefaultPathMetric, Value: &value, ObservedAt: at,
	})
}

func found(t *testing.T, g *core.Graph, opts PathOptions) map[string]Finding {
	t.Helper()
	list, err := Paths(g, opts)
	if err != nil {
		t.Fatal(err)
	}
	byRoute := map[string]Finding{}
	for _, f := range list {
		byRoute[PathLabel(g, f.Path)] = f
	}
	return byRoute
}

// A route is derived from where a request can arrive, not from every node that
// happens to call something. Rooting one at each hop would report one estate
// as a few hundred findings, most of them the tail of another.
func TestDeclaredRoutesStartWhereARequestArrives(t *testing.T) {
	g := routes()
	derived := DeclarePaths(g, DeclareOptions{})
	if len(derived) != 2 {
		t.Fatalf("got %d routes, want the two that start at the gateway: %#v", len(derived), derived)
	}
	for _, p := range derived {
		if p.Nodes[0] != "gateway" {
			t.Fatalf("a route starts at %q, which something calls", p.Nodes[0])
		}
		if p.Claim == nil || p.Claim.Note == "" {
			t.Fatal("a derived route must say it was derived")
		}
	}
}

// A route that depends on a rule the network merely permits is a reachable
// route. Calling it declared would say the configuration promises something it
// does not.
func TestARouteIsOnlyAsDeclaredAsItsWeakestHop(t *testing.T) {
	g := routes()
	for i := range g.Edges {
		if g.Edges[i].From == "reports" {
			g.Edges[i].Kind = core.EdgeReachable
		}
	}
	g.Normalize()
	for _, p := range DeclarePaths(g, DeclareOptions{}) {
		if p.Nodes[1] == "reports" && p.Kind != core.EdgeReachable {
			t.Fatalf("a route through a merely permitted hop is %s", p.Kind)
		}
		if p.Nodes[1] == "checkout" && p.Kind != core.EdgeIACRef {
			t.Fatalf("a route whose every hop is declared is %s", p.Kind)
		}
	}
}

// The three answers, on one estate: a route walked in full, a route walked as
// far as its second hop, and a route nothing has touched.
func TestARouteIsUsedPartlyUsedOrUnused(t *testing.T) {
	g := routes()
	g.Paths = DeclarePaths(g, DeclareOptions{})
	walked(g, "2026-09-02T10:00:01Z", 2, "gateway", "checkout", "ledger")
	walked(g, "2026-05-01T10:00:00Z", 1, "gateway", "reports")
	g.Normalize()

	got := found(t, g, PathOptions{})
	if _, reported := got["gateway → checkout → ledger"]; reported {
		t.Fatal("a route walked in full was reported as a finding")
	}
	partial := got["gateway → reports → archive"]
	if partial.Kind != Partial {
		t.Fatalf("a route walked as far as its second hop is %q", partial.Kind)
	}
	if partial.LastSeen != "2026-05-01T10:00:00Z" {
		t.Fatalf("the partial finding does not say when that part was last walked: %#v", partial)
	}
}

// A request that stopped early walked part of a declared route. Reporting it
// as unannounced is the false alarm that makes a listing worth ignoring.
func TestAWalkThatStoppedEarlyIsNotASurprise(t *testing.T) {
	g := routes()
	g.Paths = DeclarePaths(g, DeclareOptions{})
	walked(g, "2026-09-01T00:00:00Z", 5, "gateway", "reports")
	g.Normalize()

	for _, f := range found(t, g, PathOptions{}) {
		if f.Kind == Unexpected {
			t.Fatalf("a prefix of a declared route was reported as unannounced: %#v", f)
		}
	}
}

// The order is the whole reason a path is an entity. A request that went
// gateway, ledger is not a walk of gateway, checkout, ledger with a hop
// missing: it is a different thing happening, and it is the one worth waking
// somebody for.
func TestTheSameServicesInAnotherOrderIsAFinding(t *testing.T) {
	g := routes()
	g.Paths = DeclarePaths(g, DeclareOptions{})
	walked(g, "2026-09-03T02:13:00Z", 1, "gateway", "ledger")
	g.Normalize()

	f, ok := found(t, g, PathOptions{})["gateway → ledger"]
	if !ok || f.Kind != Unexpected {
		t.Fatalf("a route nothing declares was not reported: %#v", f)
	}
	if f.LastSeen != "2026-09-03T02:13:00Z" || f.Requests == nil || *f.Requests != 1 {
		t.Fatalf("the finding does not say when it fired or how often: %#v", f)
	}
}

// Never used and stopped being used are different facts, and only the second
// one is a change.
func TestARouteThatStoppedIsNotTheSameAsOneNeverWalked(t *testing.T) {
	g := routes()
	g.Paths = DeclarePaths(g, DeclareOptions{})
	walked(g, "2026-01-01T00:00:00Z", 900, "gateway", "checkout", "ledger")
	g.Normalize()

	got := found(t, g, PathOptions{Since: "2026-08-01T00:00:00Z"})
	quiet := got["gateway → checkout → ledger"]
	if quiet.Kind != Quiet {
		t.Fatalf("a route that stopped is %q", quiet.Kind)
	}
	if quiet.Requests == nil || *quiet.Requests != 900 {
		t.Fatalf("the finding does not carry what the last reading counted: %#v", quiet)
	}
	if got["gateway → reports → archive"].Kind != Unused {
		t.Fatalf("a route nothing ever walked should be unused, not %q", got["gateway → reports → archive"].Kind)
	}

	// Without a cutoff nothing is quiet: how long is too long is a question
	// about today, and today is not something a projection may read.
	if k := found(t, g, PathOptions{})["gateway → checkout → ledger"].Kind; k != "" {
		t.Fatalf("a route was called %q with no cutoff given", k)
	}
}

// Two runs over the same document have to produce the same list, or a finding
// is not something anybody can commit and diff.
func TestAListingIsDeterministic(t *testing.T) {
	g := routes()
	g.Paths = DeclarePaths(g, DeclareOptions{})
	walked(g, "2026-09-03T02:13:00Z", 1, "gateway", "ledger")
	g.Normalize()

	first, err := Paths(g, PathOptions{Since: "2026-08-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := Paths(g, PathOptions{Since: "2026-08-01T00:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(first) {
			t.Fatalf("got %d findings then %d", len(first), len(again))
		}
		for i := range first {
			if again[i].Key != first[i].Key || again[i].Kind != first[i].Kind {
				t.Fatalf("finding %d moved: %s/%s then %s/%s", i,
					first[i].Kind, first[i].Key, again[i].Kind, again[i].Key)
			}
		}
	}
}
