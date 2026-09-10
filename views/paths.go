package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/core"
)

// The three questions a list of routes answers.
//
// They are deliberately not severities. Which of them matters is a property of
// the estate — an unused route in a codebase being retired is the goal, and a
// route that fired unannounced in a payment system is an incident — so this
// package names what was found and leaves ranking to whoever is reading.
const (
	// Unused is a route the configuration or the network permits and nothing
	// has ever been seen walking. It is the finding an operator can act on:
	// a route nobody has walked is a route to delete.
	Unused = "unused"

	// Quiet is a route that was walked and then stopped. The difference from
	// unused is the whole point — one was never used, the other was and is
	// not, and only the second is a change.
	Quiet = "quiet"

	// Partial is a declared route something walked the beginning of and never
	// finished. Neither of the other two answers is true about it, and which
	// hop it stops at is the useful part: requests reach the ledger and never
	// go on to the archive.
	Partial = "partial"

	// Unexpected is a route something walked that no declared route contains
	// in that order. It needs no threshold and no baseline: the finding is
	// the walk itself.
	Unexpected = "unexpected"
)

// compare says what the observed routes have to say about one declared route.
//
// The comparison is by run rather than by set, because the order is the whole
// reason a path is an entity. A request that went gateway, ledger is not a
// walk of gateway, checkout, ledger with a hop missing: it is a different
// thing happening, and it is exactly the thing worth an alert.
func compare(p core.Path, observed []core.Path, readings map[string]*reading, since string) []Finding {
	key := p.Key()
	walked := false
	longest := 0
	last := ""
	var count *float64

	// The two answers are kept apart on purpose. A route walked in full is
	// answered by the walks of the whole route; how far a *different* walk got
	// along it says nothing about when this one last ran, and letting a prefix
	// overwrite the full walk's reading reported a route walked yesterday as
	// having stopped in January.
	var partialLast string
	var partialCount *float64

	for _, o := range observed {
		if containsRun(o.Nodes, p.Nodes) {
			walked = true
			if at := readings[o.Key()]; at != nil && !before(at.last, last) {
				last, count = at.last, at.count
			}
			continue
		}
		// How far along this route anything actually got. A route walked as
		// far as its second hop and never further is not unused, and saying
		// so is more useful than either of the two answers that fit in a
		// yes-or-no comparison.
		n := sharedPrefix(o.Nodes, p.Nodes)
		if n < 2 || n < longest {
			continue
		}
		// When a route was only ever walked in part, when that part was last
		// walked is the actionable half of the finding: requests stopped
		// reaching the archive in May is a different story from stopped
		// yesterday.
		if at := readings[o.Key()]; at != nil && (n > longest || !before(at.last, partialLast)) {
			partialLast, partialCount = at.last, at.count
		}
		longest = n
	}

	switch {
	case walked && since != "" && last != "" && before(last, since):
		return []Finding{{
			Kind: Quiet, Path: p, Key: key, Claim: p.Claim,
			LastSeen: last, Requests: count,
			Reason: fmt.Sprintf("last walked %s, before %s", last, since),
		}}
	case walked:
		return nil
	case longest >= 2:
		return []Finding{{
			Kind: Partial, Path: p, Key: key, Claim: p.Claim,
			LastSeen: partialLast, Requests: partialCount,
			Reason: fmt.Sprintf("walked as far as %s; nothing has been seen going on to %s",
				p.Nodes[longest-1], p.Nodes[longest]),
		}}
	default:
		return []Finding{{
			Kind: Unused, Path: p, Key: key, Claim: p.Claim,
			Reason: fmt.Sprintf("%s route, and nothing has been seen walking it", p.Kind),
		}}
	}
}

// declaredCovers reports whether any declared route contains this walk in this
// order. A request that stopped early walked part of a declared route and is
// not a surprise; one that visited the same services in another order is.
func declaredCovers(walk []string, declared []core.Path) bool {
	for _, d := range declared {
		if containsRun(d.Nodes, walk) {
			return true
		}
	}
	return false
}

// containsRun reports whether inner appears in outer as consecutive elements.
func containsRun(outer, inner []string) bool {
	if len(inner) == 0 || len(inner) > len(outer) {
		return false
	}
	for i := 0; i+len(inner) <= len(outer); i++ {
		same := true
		for j := range inner {
			if outer[i+j] != inner[j] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// sharedPrefix is how many participants two walks agree on from the start.
func sharedPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// PathFindingKinds lists what a listing can report, in the order a reader
// most often wants them: what fired unannounced, what stopped, what was never
// used.
func PathFindingKinds() []string { return []string{Unexpected, Quiet, Partial, Unused} }

// ValidPathFinding reports whether name is one of them.
func ValidPathFinding(name string) bool {
	for _, kind := range PathFindingKinds() {
		if kind == name {
			return true
		}
	}
	return false
}

// before compares two moments as moments.
//
// Comparing the strings would be nearly right, and wrong exactly where it
// matters: the trace collector writes RFC3339 with nanoseconds while a
// relative cutoff is written to the second, and "." sorts before "Z", so a
// walk at 10:00:00.5Z lands before a cutoff of 10:00:00Z and a route walked
// seconds ago is reported as having stopped. An offset like +09:00 goes wrong
// the same way.
//
// An empty moment is before every real one: a reading with nothing to say
// about when is the oldest thing there is. Anything that does not parse falls
// back to comparing the text, which is at least stable.
func before(a, b string) bool {
	if a == b {
		return false
	}
	if a == "" || b == "" {
		return a == ""
	}
	at, aerr := time.Parse(time.RFC3339, a)
	bt, berr := time.Parse(time.RFC3339, b)
	if aerr != nil || berr != nil {
		return a < b
	}
	return at.Before(bt)
}

// reading is what the observations say about one route: when it was last
// walked, and how many walks the most recent reading counted.
type reading struct {
	last  string
	count *float64
}

// Finding is one route, and what this graph has to say about it.
type Finding struct {
	Kind     string      `json:"kind"`
	Path     core.Path   `json:"path"`
	Key      string      `json:"key"`
	Reason   string      `json:"reason"`
	LastSeen string      `json:"last_seen,omitempty"`
	Requests *float64    `json:"requests,omitempty"`
	Claim    *core.Claim `json:"claim,omitempty"`
}

// PathOptions bounds a listing.
type PathOptions struct {
	// Since is an RFC3339 timestamp. A route last walked before it is quiet;
	// empty means no route is quiet, only never-walked ones are unused.
	//
	// It is a timestamp rather than a duration because nothing in this package
	// reads a clock. Two runs over the same document must produce the same
	// bytes, which is what makes a finding something you can commit, diff and
	// argue with; "thirty days" is a question about today, and today belongs
	// to the caller.
	Since string

	// Metric names the reading that counts walks. Empty means path_requests,
	// which is what the trace collector writes.
	Metric string
}

// DefaultPathMetric is the reading a path listing counts walks by.
const DefaultPathMetric = "path_requests"

// Paths reports what the declared and the observed routes say about each
// other.
//
// This is the same comparison the whole project is built on — what the
// configuration permits against what actually happened — moved from one hop up
// to a whole route. An edge that is reachable and never observed is a rule
// nobody needs; a *route* that is declared and never observed is a request
// path nobody walks, and that is the one somebody can delete.
func Paths(g *core.Graph, opts PathOptions) ([]Finding, error) {
	if g == nil {
		return nil, fmt.Errorf("no graph")
	}
	metric := opts.Metric
	if metric == "" {
		metric = DefaultPathMetric
	}

	readings := map[string]*reading{}
	for _, o := range g.Observations {
		if o.Metric != metric {
			continue
		}
		if _, ok := core.ParsePathKey(o.Subject); !ok {
			continue
		}
		at := readings[o.Subject]
		if at == nil {
			at = &reading{}
			readings[o.Subject] = at
		}
		if !before(o.ObservedAt, at.last) {
			at.last = o.ObservedAt
			at.count = o.Value
		}
	}

	var observed, declared []core.Path
	for _, p := range g.Paths {
		if p.Kind == core.EdgeObserved {
			observed = append(observed, p)
			continue
		}
		declared = append(declared, p)
	}

	// Empty rather than absent, for the same reason an alert listing is: a
	// caller reading the JSON should not have to tell "nothing to report" from
	// "this field is missing".
	out := []Finding{}
	for _, p := range g.PathsOfKind(core.EdgeIACRef) {
		out = append(out, compare(p, observed, readings, opts.Since)...)
	}
	for _, p := range g.PathsOfKind(core.EdgeReachable) {
		out = append(out, compare(p, observed, readings, opts.Since)...)
	}
	for _, p := range g.PathsOfKind(core.EdgeObserved) {
		if declaredCovers(p.Nodes, declared) {
			continue
		}
		f := Finding{
			Kind: Unexpected, Path: p, Key: p.Key(), Claim: p.Claim,
			Reason: "something walked this route and no declared route contains it in this order",
		}
		if at := readings[p.Key()]; at != nil {
			f.LastSeen, f.Requests = at.last, at.count
		}
		out = append(out, f)
	}

	// Grouped by what was found and then by the route itself, so that two runs
	// over the same document produce the same list and a diff between two
	// documents is a list of what changed.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

type hop struct {
	to   string
	kind core.EdgeKind

	// rules are the routing rules this hop came in through, when the document
	// records any — the hosts and paths an Ingress matched. They are only
	// meaningful on the first hop of a walk: that is the one that says how a
	// request got in, and everything after it is one service calling another.
	rules []string
}

// callGraph reads the declared calls out of a document: what leads where, and
// which of those are where a route can start.
//
// It is separate from DeclarePaths because two things need the same reading of
// the same edges — the derivation itself, and the explanation of why it found
// nothing. An explanation worked out from a second, similar scan is an
// explanation that goes wrong the first time somebody changes what counts as a
// call.
func callGraph(g *core.Graph) (next map[string][]hop, roots []string, calls int) {
	next = map[string][]hop{}
	called := map[string]bool{}
	starts := map[string]bool{}
	for _, e := range g.Edges {
		if e.Suppressed || e.Kind == core.EdgeObserved || !isCall(e) {
			continue
		}
		if _, ok := g.Node(e.From); !ok {
			continue
		}
		if _, ok := g.Node(e.To); !ok {
			continue
		}
		next[e.From] = append(next[e.From], hop{to: e.To, kind: e.Kind, rules: routingRules(e)})
		called[e.To] = true
		starts[e.From] = true
		calls++
	}
	roots = make([]string, 0, len(starts))
	for id := range starts {
		if !called[id] {
			roots = append(roots, id)
		}
	}
	sort.Strings(roots)
	return next, roots, calls
}

// Why following references produced no route at all. The two are different
// situations with different things to do about them, and one message covering
// both sends half its readers looking for a cycle that is not there.
const (
	// NoReferences: there is not a single declared call in this document to
	// follow. A graph built from traces alone is the ordinary way to arrive
	// here, and it is not a defect — there is simply no declared side to
	// derive from.
	NoReferences = "no-references"

	// NoStart: there are declared calls, but everything they lead to is also
	// called by something else, so there is nowhere a route begins. An estate
	// whose entry point sits in a cycle looks like this.
	NoStart = "no-start"
)

// WhyNoDeclaredPaths names which of the two happened. It is only meaningful
// when DeclarePaths returned nothing; it returns "" otherwise.
func WhyNoDeclaredPaths(g *core.Graph) string {
	_, roots, calls := callGraph(g)
	switch {
	case calls == 0:
		return NoReferences
	case len(roots) == 0:
		return NoStart
	}
	return ""
}

// DeclarePaths derives the routes the declared references permit.
//
// # Why this is derivation and not invention
//
// Nothing in a Terraform plan or a manifest says "a request enters here and
// ends there". What they say is that this one references that one, and the
// route is what you get by following those references from wherever a request
// can arrive. That is a reading of claims somebody else made, not a new claim,
// and it carries a note saying so — the same footing the atlas puts a derived
// sequence on.
//
// It is not written into the graph by any parser. A declared route somebody
// actually wrote down — in an overlay, from an API definition, from a routing
// table — is a better claim than this one, and when a document already carries
// declared routes this function is not what a listing should use.
//
// # Where a route starts
//
// At something nothing calls. A service everything calls is not where a
// request enters the estate, and rooting a route there would produce a list
// where every hop is also the start of its own route — one estate reported as
// a few hundred findings, most of them the tail of another.
func DeclarePaths(g *core.Graph, opts DeclareOptions) []core.Path {
	depth, limit := opts.Depth, opts.Limit
	if depth <= 0 {
		depth = defaultDeclareDepth
	}
	if limit <= 0 {
		limit = defaultDeclareLimit
	}

	next, roots, _ := callGraph(g)

	var out []core.Path
	var walk func(chain []string, kind core.EdgeKind, entry []string, visited map[string]bool)
	walk = func(chain []string, kind core.EdgeKind, entry []string, visited map[string]bool) {
		if len(out) >= limit {
			return
		}
		at := chain[len(chain)-1]
		hops := append([]hop(nil), next[at]...)
		sort.SliceStable(hops, func(i, j int) bool {
			if hops[i].to != hops[j].to {
				return hops[i].to < hops[j].to
			}
			return hops[i].kind < hops[j].kind
		})

		walked := false
		for _, h := range hops {
			if visited[h.to] || len(chain) >= depth {
				continue
			}
			walked = true
			// A route is only as declared as its weakest hop. One that
			// depends on a rule the network merely permits is a reachable
			// route, and calling it declared would say the configuration
			// promises something it does not.
			step := kind
			if h.kind == core.EdgeReachable {
				step = core.EdgeReachable
			}
			// Where the request came in. It is the first hop's rule and
			// nothing else's: a route is one way into the estate followed by
			// one service calling another, and a rule further down would be a
			// second way in rather than part of this one.
			came := entry
			if len(chain) == 1 {
				came = h.rules
			}
			visited[h.to] = true
			walk(append(chain, h.to), step, came, visited)
			delete(visited, h.to)
		}
		if walked || len(chain) < 2 || len(out) >= limit {
			return
		}
		route := make([]string, len(chain))
		copy(route, chain)
		p := core.Path{
			Nodes: route, Kind: kind,
			Claim: &core.Claim{Origin: core.OriginParser, Note: "derived from declared references"},
		}
		// The host and path a request arrives on is what somebody means when
		// they ask which API is unused. Dropping it left a listing that could
		// only say which boxes were involved, which is a different question
		// and not the one that was asked.
		if len(entry) > 0 {
			p.Attrs = map[string]any{"entry": append([]string(nil), entry...)}
		}
		out = append(out, p)
	}
	for _, root := range roots {
		walk([]string{root}, core.EdgeIACRef, nil, map[string]bool{root: true})
	}
	return out
}

// DeclareOptions bounds the derivation, on the same terms an atlas is bounded:
// a call chain stops being readable long before it stops being followable, and
// an estate can have more routes than anybody will read.
type DeclareOptions struct {
	Depth int
	Limit int
}

const (
	defaultDeclareDepth = 6
	defaultDeclareLimit = 500
)

// routingRules are the rules an edge says a request arrives by.
//
// Only from an edge that routes. `via` is a general "how did this come to
// exist" note that half the Kubernetes parser writes — a TLS secret, an
// envFrom key, a NetworkPolicy — and reading it wherever it appears turned
// `web reads app-config` into an API somebody could be asked why nobody uses.
func routingRules(e core.Edge) []string {
	if e.Attrs == nil || !strings.Contains(strings.ToLower(e.Relation), "route") {
		return nil
	}
	// From `rules` and nowhere else.
	//
	// `via` says how the edge came to exist in words, and the words include
	// ways in that are not an API path — a default backend, a rule matching
	// any host. Falling back to them put "default backend" where a consumer
	// was promised something it could match an API against, which is worse
	// than saying nothing: an entry that cannot be matched is not a smaller
	// answer, it is a wrong one.
	return stringsOf(e.Attrs["rules"])
}

// stringsOf reads a list of strings out of an attribute, whether it arrived as
// one or came back through JSON as a list of anything.
func stringsOf(v any) []string {
	switch list := v.(type) {
	case []string:
		return append([]string(nil), list...)
	case []any:
		out := make([]string, 0, len(list))
		for _, one := range list {
			if s, ok := one.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// EntryOf is where a route was entered — the hosts and paths an Ingress or a
// routing rule matched — or empty when nothing said.
//
// A list rather than one joined string: "which API is unused" is a question
// something asks of the JSON, and an answer of "shop.example.com/checkout,
// shop.example.com/checkout/v2" cannot be matched against either of them.
func EntryOf(p core.Path) []string {
	if p.Attrs == nil {
		return nil
	}
	return stringsOf(p.Attrs["entry"])
}

// PathLabel is a route written the way somebody says it out loud.
//
// A route that says where a request came in says it first. "Which API is
// nobody using" is the question being asked, and a line that could only name
// the boxes involved was answering a different one — while a line that named
// only the API would have dropped what it goes through, which is the other
// half of the same answer.
func PathLabel(g *core.Graph, p core.Path) string {
	if p.Label != "" {
		return p.Label
	}
	if entry := EntryOf(p); len(entry) > 0 {
		return strings.Join(entry, ", ") + ": " + walkLabel(g, p)
	}
	return walkLabel(g, p)
}

func walkLabel(g *core.Graph, p core.Path) string {
	names := make([]string, 0, len(p.Nodes))
	for _, id := range p.Nodes {
		if n, ok := g.Node(id); ok && n.Name != "" {
			names = append(names, n.Name)
			continue
		}
		names = append(names, id)
	}
	out := names[0]
	for _, name := range names[1:] {
		out += " → " + name
	}
	return out
}
