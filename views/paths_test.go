package views

import (
	"strings"
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

// Deriving nothing has two causes and they are not the same one. A graph built
// from traces alone has no declared call to follow, and there is nothing wrong
// with it. A graph full of declared calls where each one leads to something
// also called has its entry point inside a cycle, which is a real thing to go
// and look at. One message covering both sends half its readers hunting for a
// cycle that is not there.
func TestWhyNothingCouldBeDerivedSaysWhichOfTheTwo(t *testing.T) {
	traced := core.New()
	for _, id := range []string{"gateway", "checkout"} {
		traced.Nodes = append(traced.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	traced.Paths = []core.Path{{Nodes: []string{"gateway", "checkout"}, Kind: core.EdgeObserved}}
	traced.Normalize()

	if got := WhyNoDeclaredPaths(traced); got != NoReferences {
		t.Errorf("a graph with nothing to follow is reported as %q", got)
	}

	cycle := core.New()
	for _, id := range []string{"a", "b"} {
		cycle.Nodes = append(cycle.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	cycle.Edges = []core.Edge{
		{From: "a", To: "b", Kind: core.EdgeIACRef, Relation: "calls"},
		{From: "b", To: "a", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	cycle.Normalize()

	if len(DeclarePaths(cycle, DeclareOptions{})) != 0 {
		t.Fatal("the fixture is not the case it is meant to be")
	}
	if got := WhyNoDeclaredPaths(cycle); got != NoStart {
		t.Errorf("a graph whose entry point is in a cycle is reported as %q", got)
	}

	// And it says nothing at all when there was nothing to explain.
	ok := watched()
	ok.Paths = nil
	if got := WhyNoDeclaredPaths(ok); got != "" {
		t.Errorf("a graph that derives routes fine is explained as %q", got)
	}
}

// routed is an estate a request enters through a routing rule: an ingress that
// matches a host and a path, then a service that calls a database.
func routed() *core.Graph {
	g := core.New()
	for _, id := range []string{"ingress:shop", "svc:checkout", "db:orders"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Edges = []core.Edge{
		{From: "ingress:shop", To: "svc:checkout", Kind: core.EdgeIACRef, Relation: "routes",
			Attrs: map[string]any{
				"via":   "shop.example.com/checkout",
				"rules": []string{"shop.example.com/checkout"},
			}},
		{From: "svc:checkout", To: "db:orders", Kind: core.EdgeIACRef, Relation: "calls"},
	}
	g.Normalize()
	return g
}

// "Which API is nobody using" is the question being asked. A listing that
// could only name the boxes involved was answering a different one — the host
// and path a request arrives on is on the edge, and the derivation dropped it.
func TestADerivedRouteSaysWhereARequestCameIn(t *testing.T) {
	g := routed()
	routes := DeclarePaths(g, DeclareOptions{})
	if len(routes) != 1 {
		t.Fatalf("got %d routes: %#v", len(routes), routes)
	}
	if got := EntryOf(routes[0]); len(got) != 1 || got[0] != "shop.example.com/checkout" {
		t.Fatalf("the route does not say where it was entered: %q", got)
	}
	// And the line says both: the API, and what it goes through. Naming only
	// the API would drop the other half of the same answer.
	g.Paths = routes
	label := PathLabel(g, routes[0])
	for _, want := range []string{"shop.example.com/checkout", "svc:checkout", "db:orders"} {
		if !strings.Contains(label, want) {
			t.Errorf("the label does not carry %q: %q", want, label)
		}
	}
}

// The entry is the first hop's rule and nothing else's. A route is one way into
// the estate followed by one service calling another, and a rule further down
// would be a second way in rather than part of this one.
func TestTheEntryIsTheFirstHopsRule(t *testing.T) {
	g := routed()
	g.Edges = append(g.Edges, core.Edge{
		From: "db:orders", To: "svc:archive", Kind: core.EdgeIACRef, Relation: "routes",
		Attrs: map[string]any{
			"via":   "internal.example.com/archive",
			"rules": []string{"internal.example.com/archive"},
		},
	})
	g.Nodes = append(g.Nodes, core.Node{ID: "svc:archive", Type: "service", Name: "archive"})
	g.Normalize()

	routes := DeclarePaths(g, DeclareOptions{})
	if len(routes) != 1 {
		t.Fatalf("got %d routes: %#v", len(routes), routes)
	}
	if got := EntryOf(routes[0]); len(got) != 1 || got[0] != "shop.example.com/checkout" {
		t.Errorf("a rule further down became the way in: %q", got)
	}
}

// A route nothing said anything about says nothing about where it came in,
// rather than an empty prefix on every line.
func TestARouteWithNoRuleSaysNothingAboutOne(t *testing.T) {
	g := core.New()
	for _, id := range []string{"a", "b"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Edges = []core.Edge{{From: "a", To: "b", Kind: core.EdgeIACRef, Relation: "calls"}}
	g.Normalize()

	routes := DeclarePaths(g, DeclareOptions{})
	if len(routes) != 1 {
		t.Fatalf("got %d routes", len(routes))
	}
	if got := EntryOf(routes[0]); len(got) != 0 {
		t.Errorf("a route invented a way in: %q", got)
	}
	if got := PathLabel(g, routes[0]); got != "a → b" {
		t.Errorf("the label carries a prefix nobody wrote: %q", got)
	}
}

// `via` is a general "how did this come to exist" note that half the Kubernetes
// parser writes — a TLS secret, an envFrom key, a NetworkPolicy — and reading it
// wherever it appears turned `web reads app-config` into an API somebody could
// be asked why nobody uses.
func TestOnlyAnEdgeThatRoutesSaysHowARequestArrived(t *testing.T) {
	g := core.New()
	for _, id := range []string{"web", "app-config"} {
		g.Nodes = append(g.Nodes, core.Node{ID: id, Type: "service", Name: id})
	}
	g.Edges = []core.Edge{{
		From: "web", To: "app-config", Kind: core.EdgeIACRef, Relation: "reads",
		Attrs: map[string]any{"via": "envFrom"},
	}}
	g.Normalize()

	routes := DeclarePaths(g, DeclareOptions{})
	if len(routes) != 1 {
		t.Fatalf("got %d routes", len(routes))
	}
	if got := EntryOf(routes[0]); len(got) != 0 {
		t.Fatalf("reading a config map became an API: %q", got)
	}
	if got := PathLabel(g, routes[0]); got != "web → app-config" {
		t.Errorf("the label carries something nobody called an entry: %q", got)
	}
}

// Every rule is its own entry. "Which API is unused" is a question something
// asks of the JSON, and an answer of "a, b" cannot be matched against either
// of them.
func TestTheEntriesAreAListAndNotOneJoinedString(t *testing.T) {
	g := routed()
	for i := range g.Edges {
		if g.Edges[i].Relation == "routes" {
			g.Edges[i].Attrs = map[string]any{
				"via":   "shop.example.com/checkout, shop.example.com/checkout/v2",
				"rules": []string{"shop.example.com/checkout", "shop.example.com/checkout/v2"},
			}
		}
	}
	g.Normalize()

	routes := DeclarePaths(g, DeclareOptions{})
	if len(routes) != 1 {
		t.Fatalf("got %d routes", len(routes))
	}
	entry := EntryOf(routes[0])
	if len(entry) != 2 {
		t.Fatalf("the entries are %q", entry)
	}
	for i, want := range []string{"shop.example.com/checkout", "shop.example.com/checkout/v2"} {
		if entry[i] != want {
			t.Errorf("entry %d is %q, want %q", i, entry[i], want)
		}
	}
}

// An entry comes from `rules` and nowhere else.
//
// `via` says how the edge came to exist in words, and the words include ways in
// that are not an API path — a default backend, a rule matching any host.
// Reading them put "default backend" where a consumer was promised something it
// could match an API against, which is worse than saying nothing: an entry that
// cannot be matched is not a smaller answer, it is a wrong one.
func TestWordsAreNotAnEntry(t *testing.T) {
	g := routed()
	for i := range g.Edges {
		if g.Edges[i].Relation == "routes" {
			g.Edges[i].Attrs = map[string]any{"via": "default backend"}
		}
	}
	g.Normalize()

	routes := DeclarePaths(g, DeclareOptions{})
	if got := EntryOf(routes[0]); len(got) != 0 {
		t.Errorf("a description became an API path: %q", got)
	}
	if got := PathLabel(g, routes[0]); got != "ingress:shop → svc:checkout → db:orders" {
		t.Errorf("the label carries something nobody can match: %q", got)
	}
}
