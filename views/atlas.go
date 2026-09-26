package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// An atlas is the answer to the complaint that one drawing of a whole estate
// is not readable. It is a bound set of diagrams with the ways between them
// written down: every box that has an inside says which diagram *is* its
// inside, so a reader descends by clicking rather than by rebuilding the
// picture with different flags.
//
// The rule that keeps this from becoming a pile of unrelated pictures is that
// every diagram in an atlas is derived from one evidence graph by a
// transformation in this package. Nothing here invents a relationship: a
// sequence is an ordering imposed on edges that were already claimed, a level
// is a projection of containment that was already recorded, and both carry
// the claim of whatever produced the edge underneath.
//
// # Why a level is flat
//
// A level diagram draws the containers directly inside it as single boxes
// rather than nesting their contents. That is the whole difference between an
// atlas and the single page it replaces: the nested drawing shows a hundred
// namespaces and everything in them at once, and the answer to "what is in
// this namespace" is a picture you have to find rather than one you open.
//
// Nesting is not lost; it moved into navigation. A container box opens the
// level below it, and the trail back up is the containment chain.

// Kind is what a diagram is, in the vocabulary a reader already has. The set
// is open in the schema and closed here: a kind exists once something can
// derive it from the IR, because a kind nothing produces is a promise rather
// than a feature.
type Kind string

const (
	// KindPackage is a level whose members are mostly containers: the
	// namespace list, the account list, the list of modules.
	KindPackage Kind = "package"

	// KindArchitecture is a level whose members are mostly resources.
	KindArchitecture Kind = "architecture"

	// KindDetail is one element and what it holds or touches — the inside of
	// a box, for the reader who clicked it asking "and what is in there".
	KindDetail Kind = "detail"

	// KindCommunication is the calls around one element, which is the same
	// shape a UML communication diagram has and is derived from the same
	// edges a sequence is.
	KindCommunication Kind = "communication"

	// KindSequence is one call chain in order. Where the order came from is on
	// the diagram; see Diagram.Order.
	KindSequence Kind = "sequence"

	// KindCodemap is one repository's code as a map: what calls what, and
	// what it imports. It is the page behind a container, for the reader who
	// clicked a box asking "and what does this actually do".
	KindCodemap Kind = "codemap"

	// KindClass is one type: what it declares, and the other types it says
	// something about. It is the same shape a UML class diagram has, and it
	// is derived from what the declarations state — never from comparing
	// method sets, which is a type checker's job.
	KindClass Kind = "class"
)

// The code graph's own vocabulary. A class diagram is a question about types,
// and these are what a document has to carry for one to be derivable at all;
// see docs/code.md.
const (
	codeType     = "code_type"
	codeFunction = "code_function"
	codeFile     = "code_file"
	codePackage  = "code_package"

	// apiOperation is one operation of a surface. Here for the same reason
	// repositoryType is: a page draws what a graph holds, whoever wrote it.
	apiOperation = "api"

	// repositoryType is a node standing for a repository a build record named.
	// The string rather than the enrichers' constant: views reads graphs, and
	// importing an enricher to learn one node type would make the projection
	// depend on the thing that produced its input.
	repositoryType = "repository"

	relDeclares = "declares"
	relCalls    = "calls"
	relImports  = "imports"

	// relServes is a claim somebody wrote down: this function answers this
	// operation. Nothing derives it — neither reader knows the other half —
	// so it arrives from an overlay. See docs/api.md.
	relServes = "serves"
)

// codeLines is what a code map draws: the relations, the kinds of box each one
// joins, and whether the far end is this repository's code.
//
// One table because two readings of it that disagree are a box put on a page
// for a line the page then declines to draw, or a line drawn to a box that was
// never chosen. Both questions are asked here — CodeOf chooses, joins draws —
// and the next relation to land on a map (a router registration, a gRPC
// service) is a row rather than an edit in two places that have to agree.
// codeLineRow is one kind of line a code map draws.
type codeLineRow struct {
	relation string
	from, to string

	// ours says the far end is code of the same repository. A serves claim's
	// is not: an operation belongs to the document that declared it, and the
	// map draws it as what a line runs to rather than as one of its own boxes.
	ours bool
}

var codeLines = []codeLineRow{
	{relation: relImports, from: codeFile, to: codePackage, ours: true},
	{relation: relCalls, from: codeFunction, to: codeFunction, ours: true},
	{relation: relServes, from: codeFunction, to: apiOperation},
}

// farType reports whether a box of this type is one a code line may reach
// outside the repository it is drawn for. Asked of the table each time rather
// than derived into a map beside it: the table has three rows, and a copy is
// a second thing to keep in step with it.
func farType(t string) bool {
	for _, l := range codeLines {
		if !l.ours && l.to == t {
			return true
		}
	}
	return false
}

// codeLine is the row a relation is drawn by, if it is drawn at all. Folded,
// because a graph is a document and somebody else may have written it.
func codeLine(relation string) (int, bool) {
	for i := range codeLines {
		if strings.EqualFold(relation, codeLines[i].relation) {
			return i, true
		}
	}
	return 0, false
}

// Where a sequence's order came from.
const (
	// OrderObserved means something walked this route and the document
	// records it as a path.
	OrderObserved = "observed"

	// OrderDerived means this package read it off the declared references: A
	// calls B and B calls C, so a request probably goes A, B, C. Nobody saw
	// it happen.
	OrderDerived = "derived"
)

// Opening is a way down: clicking Element in the diagram that carries this
// opening arrives at Diagram.
//
// It is recorded per diagram rather than computed by the viewer because the
// question "does this box have an inside" is a property of the derivation,
// not of the picture. A viewer that guessed would offer a door into an empty
// room, and a reader who opens two empty rooms stops trying the third.
type Opening struct {
	Element string `json:"element"`
	Diagram string `json:"diagram"`
	Kind    Kind   `json:"kind"`
	Label   string `json:"label,omitempty"`
}

// Diagram is one page of an atlas: a projected graph, what it is, and where a
// reader can go from it.
type Diagram struct {
	ID       string `json:"id"`
	Kind     Kind   `json:"kind"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`

	// Parent is the diagram this one was opened from, and "" for the root.
	// It is the trail back up, and it is single because every diagram here is
	// reached by descending: two parents would mean the same page means two
	// different things depending on how it was entered.
	Parent string `json:"parent,omitempty"`

	// Origin is the element in the parent whose inside this diagram is.
	Origin string `json:"origin,omitempty"`

	// Order is where a sequence's order came from. Empty on every other kind
	// of page, because only a sequence has one.
	//
	// It is a field rather than a sentence in the subtitle because a reader
	// has to be able to tell the two apart at a glance, and a viewer has to
	// be able to draw them differently. "A request went this way" and "the
	// references say a request could go this way" are different claims, and
	// this project exists to keep those apart.
	Order string `json:"order,omitempty"`

	Graph *core.Graph `json:"graph"`
	Opens []Opening   `json:"opens,omitempty"`
}

// Atlas is the whole bound set.
type Atlas struct {
	Version  string    `json:"version"`
	Root     string    `json:"root"`
	Diagrams []Diagram `json:"diagrams"`
}

// AtlasVersion is bumped when the shape of the document changes
// incompatibly, on the same terms as the IR's own version.
const AtlasVersion = "0.1"

// AtlasOptions bounds the derivation. Every field has a working default,
// because the common call is "build the atlas for this graph".
type AtlasOptions struct {
	// Axis is the containment axis the levels are built from. Empty means the
	// document's default, which is the network axis when it has one.
	Axis string

	// Depth bounds a sequence. A call chain in a real estate can be as long
	// as the estate, and a sequence diagram stops being readable long before
	// that.
	Depth int

	// Limit bounds the number of diagrams. An atlas derives a page per node,
	// so an estate of ten thousand resources would otherwise produce a
	// document nobody can load — and the reader who needs that estate needs a
	// filtered graph first, not a bigger atlas.
	Limit int
}

const (
	defaultSequenceDepth = 5
	defaultAtlasLimit    = 400
)

// RootDiagram is the id of the level every atlas starts at.
const RootDiagram = "level:"

// calls are the relations that mean "this one asked that one to do
// something". They are what a sequence is derived from, and what makes a
// detail diagram a communication diagram instead.
//
// Containment relations are deliberately absent: a workload mounting a volume
// is not a message, and a sequence built from one would read as a call that
// nothing claimed.
var callRelations = []string{"call", "invoke", "request", "route", "trigger", "publish", "subscribe", "query", "read", "write"}

// holds are the relations that mean "that one is inside this one" — the
// reason a box has an inside at all beyond the containment axis. An EC2
// instance running three applications records that as edges, not as a group,
// because the applications came from a different input than the instance did.
//
// Matched exactly, unlike the call relations, because containment is the
// claim that gets drawn as nesting and a near miss is drawn as a lie. On
// substring terms `runs-as` — a workload naming its ServiceAccount — reads as
// `runs`, and the account is then drawn inside the workload it merely
// authenticates as.
var holdRelations = map[string]bool{
	"owns": true, "hosts": true, "runs": true, "manages": true,
	"contains": true, "deploys": true, "selects": true,
}

// reversedHolds are relations recorded from the child to the parent. An
// ownerReference points at the owner, so the edge runs the wrong way for the
// question "what is inside this".
//
// `scales` is deliberately in neither table: an autoscaler is not where its
// workload lives, in either direction.
var reversedHolds = map[string]bool{"owned-by": true, "governed-by": true}

// BuildAtlas derives the bound set from one evidence graph.
//
// The shape is a tree: a level per container, a detail page per node that has
// something to show, and a sequence per element that starts a call chain.
// Every id is derived from the element it belongs to, so the same graph
// produces the same atlas — which is what lets an atlas be committed and
// diffed like the SVG already is.
func BuildAtlas(in *core.Graph, opts AtlasOptions) (*Atlas, error) {
	if in == nil {
		return nil, fmt.Errorf("no graph")
	}
	axis := in.AxisOrDefault(opts.Axis)
	if opts.Axis != "" && axis == "" {
		return nil, fmt.Errorf("this graph has no %s axis", opts.Axis)
	}
	// Nobody said which axis, and the code's own axis came first in the list.
	// An estate that has read a repository has both, and defaulting to the
	// code's meant the front page of the estate was the repository's
	// directories — the code is a guest here, and `--axis source` is how a
	// reader asks for it.
	if opts.Axis == "" && axis == core.AxisSource {
		for _, a := range in.Axes {
			if a.ID != core.AxisSource {
				axis = a.ID
				break
			}
		}
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = defaultSequenceDepth
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultAtlasLimit
	}

	b := &builder{in: in, axis: axis, depth: depth, limit: limit, seen: map[string]bool{}}
	b.readDeclarations()
	if err := b.level("", "", ""); err != nil {
		return nil, err
	}
	b.prune()
	if err := b.liftCode(); err != nil {
		return nil, err
	}
	sort.SliceStable(b.out, func(i, j int) bool { return b.out[i].ID < b.out[j].ID })
	return &Atlas{Version: AtlasVersion, Root: RootDiagram, Diagrams: b.out}, nil
}

// prune removes the ways that lead nowhere.
//
// A page records what it opens before the pages behind it are built, so the
// bound reached in the middle of that leaves openings pointing at diagrams
// that do not exist. Both halves of the viewer take that at face value: it
// draws the chevron and the button, and clicking either does nothing at all —
// which is worse than the door not being there, because the reader concludes
// the page is broken rather than that the estate was too big.
//
// The same bound can orphan a page whose parent level was never reached, and
// an orphan cannot be navigated back out of. It is dropped with its subtree
// rather than re-parented: the parent chain is containment, and a page hung
// off the nearest surviving ancestor would say something about where the
// element lives that is not true.
func (b *builder) prune() {
	for {
		alive := make(map[string]bool, len(b.out))
		for _, d := range b.out {
			alive[d.ID] = true
		}
		kept := b.out[:0]
		dropped := false
		for _, d := range b.out {
			if d.Parent != "" && !alive[d.Parent] {
				dropped = true
				continue
			}
			kept = append(kept, d)
		}
		b.out = kept
		if !dropped {
			break
		}
	}

	alive := make(map[string]bool, len(b.out))
	for _, d := range b.out {
		alive[d.ID] = true
	}
	for i := range b.out {
		opens := b.out[i].Opens[:0]
		for _, o := range b.out[i].Opens {
			if alive[o.Diagram] {
				opens = append(opens, o)
			}
		}
		if len(opens) == 0 {
			b.out[i].Opens = nil
			continue
		}
		b.out[i].Opens = opens
	}
}

type builder struct {
	in    *core.Graph
	axis  string
	depth int
	limit int

	out  []Diagram
	seen map[string]bool

	// declares is what each type declares, read out of the document once.
	//
	// Once, because a class page asks the question for every member of every
	// type and detail recurses over every node — asking it by walking the
	// edges each time turns an estate into a quadratic one. And read rather
	// than compared: the relation is matched the way holdsFrom and isCall
	// match theirs, without regard to case, so a document that writes
	// "Declares" does not lose every method it names.
	declares map[string]map[string]bool

	// code is what each named input holds, read out of the document once per
	// input for the same reason.
	code map[string][]string

	// byName is one repository's whole code map: every input that says it is
	// that repository, read together and once.
	byName map[string][]string

	// descended records the code maps whose members have already been walked.
	descended map[string]bool

	// nodes is the document's nodes by id. A graph is a list, so looking one
	// up walks it, and every page asks about a node per edge it considers —
	// a cost that is invisible on an estate and quadratic on a repository.
	nodes map[string]*core.Node

	// incident is the edges at each node, by index into the document's own
	// list so that the edge is read from there and never copied stale.
	incident map[string][]int
}

// touching is the index of every edge with this node at either end.
//
// A page asks what one element is next to, and asking it of the whole document
// is a walk of every edge — fine for a page and not for a page per member of a
// code map, where the same walk happens once for every function a repository
// has. The index is built once and read by every page after it.
func (b *builder) touching(id string) []int {
	if b.incident == nil {
		b.incident = make(map[string][]int, len(b.in.Nodes))
		for i, e := range b.in.Edges {
			b.incident[e.From] = append(b.incident[e.From], i)
			if e.To != e.From {
				b.incident[e.To] = append(b.incident[e.To], i)
			}
		}
	}
	return b.incident[id]
}

// placesCode reports whether the axis being drawn is the code's own.
//
// An atlas on the source axis is the code's structure: directories are
// containers, and a package or a file directly under the repository is at the
// root of that axis because that is where it is, not because the axis had
// nothing to say about it. Stripping it there takes the packages, and the
// lines lifted to them, off the top page of an atlas whose whole subject is
// the code.
//
// The axis itself, rather than whether some node of that input happens to
// carry a group on it. That was tried and answered a different question: a
// repository whose files all sit at its root has no directories, so no node
// carried a group and the code was stripped off the source axis after all —
// while on an estate's axis a single function somebody had put in a namespace
// answered yes for the whole repository and brought the rest back onto the
// front page.
func (b *builder) placesCode() bool {
	return b.axis == core.AxisSource
}

// node is the document's node with this id.
func (b *builder) node(id string) (*core.Node, bool) {
	if b.nodes == nil {
		b.nodes = make(map[string]*core.Node, len(b.in.Nodes))
		for i := range b.in.Nodes {
			b.nodes[b.in.Nodes[i].ID] = &b.in.Nodes[i]
		}
	}
	n, ok := b.nodes[id]
	return n, ok
}

// liftCode takes the code off the estate's front page, once the atlas is
// built and pruned and it is known where else it is drawn.
//
// Source carries no position on an estate's axis — a function is not in a
// namespace, a subscription or a VPC — and a level page draws what the axis
// places nowhere at its root, because "nowhere on this axis" and "at the top
// of this axis" are one thing to it. A repository of any size then arrives as
// a mat of boxes on the front page of the estate, which is the complaint the
// atlas exists to answer rather than to reproduce.
//
// Only a box that a code map of this atlas draws, or that a page one of those
// maps opens draws. Anything looser takes the box off this page and leaves it
// on a page nothing opens: the door it had was the one being taken away.
// "Drawn somewhere" is not the test — "drawn somewhere a reader can get to
// without this door" is.
//
// After prune rather than before, because a map whose level was never built is
// dropped there, and a box lifted on the strength of it would be lifted onto
// nothing.
func (b *builder) liftCode() error {
	if b.placesCode() {
		return nil
	}
	at := map[string]int{}
	for i := range b.out {
		at[b.out[i].ID] = i
	}
	elsewhere := map[string]bool{}
	for i := range b.out {
		if b.out[i].Kind != KindCodemap {
			continue
		}
		for _, n := range b.out[i].Graph.Nodes {
			elsewhere[n.ID] = true
		}
		// And the pages the map opens: what a mapped file declares is drawn
		// there rather than on the map, and the map is the door to it. Every
		// one of those doors is a member's — the map gives an operation none —
		// so the walk below is over this repository's own pages.
		//
		// On them, everything that is not somebody else's code. A page reached
		// from here shows its subject's neighbours, and a neighbour can be
		// anybody's: a function's page draws what calls it across a repository
		// boundary. Lifting one of those is this pass inverted — a box taken
		// off the front page on the strength of a map it is not on, and which
		// will never be built for it because nobody placed its repository.
		//
		// Not of *another* input, rather than of this one: a code node with no
		// input stamped on it is nowhere else to be found, so it belongs
		// behind the box whose page it was drawn on. Asking for this
		// repository's stamp instead left every such node on the front page
		// and on the map both, which is the duplication this whole pass is
		// about.
		of := b.scopesOf(strings.TrimPrefix(b.out[i].ID, codemapID("")))
		for _, open := range b.out[i].Opens {
			j, ok := at[open.Diagram]
			if !ok {
				continue
			}
			for _, n := range b.out[j].Graph.Nodes {
				if from, _ := n.Attrs["repository"].(string); from == "" || of[from] {
					elsewhere[n.ID] = true
				}
			}
		}
	}
	if len(elsewhere) == 0 {
		return nil
	}

	// Every level, not only the estate's front page. Code carries no position
	// on an estate's axis and lands at its root, but a graph can put it
	// anywhere — an overlay may place a function in a namespace — and then it
	// was drawn on that level and on the map both.
	for i := range b.out {
		if !strings.HasPrefix(b.out[i].ID, levelID("")) {
			continue
		}
		if err := b.liftFrom(&b.out[i], elsewhere); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) liftFrom(d *Diagram, elsewhere map[string]bool) error {
	gone := map[string]bool{}
	kept := d.Graph.Nodes[:0]
	for _, n := range d.Graph.Nodes {
		if elsewhere[n.ID] && isCode(n.Type) {
			gone[n.ID] = true
			continue
		}
		kept = append(kept, n)
	}
	if len(gone) == 0 {
		return nil
	}
	d.Graph.Nodes = kept

	// A line to a box that is not here is not here either. It is on the page
	// that does draw the box, between it and whatever it is joined to there.
	edges := d.Graph.Edges[:0]
	for _, e := range d.Graph.Edges {
		if gone[e.From] || gone[e.To] {
			continue
		}
		edges = append(edges, e)
	}
	d.Graph.Edges = edges

	opens := d.Opens[:0]
	for _, open := range d.Opens {
		if gone[open.Element] {
			continue
		}
		opens = append(opens, open)
	}
	d.Opens = opens

	// What was carried onto this page is carried again, because some of it was
	// about a box that is no longer here. An observation about a node the page
	// does not draw is a dangling reference, and core.Validate rejects the
	// whole document for one — which on this path is the whole atlas, over a
	// page that had simply been tidied.
	d.Graph.Observations, d.Graph.LogRecords, d.Graph.Conflicts = nil, nil, nil
	carry(b.in, d.Graph)

	containers := 0
	for _, n := range d.Graph.Nodes {
		if held, _ := n.Attrs["container"].(bool); held {
			containers++
		}
	}
	d.Kind = levelKind(containers, len(d.Graph.Nodes)-containers)
	d.Subtitle = fmt.Sprintf("%d containers · %d resources", containers, len(d.Graph.Nodes)-containers)

	d.Graph.Normalize()
	if err := d.Graph.Validate(); err != nil {
		return fmt.Errorf("level %q: %w", d.ID, err)
	}
	return nil
}

func isCode(t string) bool {
	switch t {
	case codeFile, codePackage, codeFunction, codeType:
		return true
	}
	return false
}

// room reports whether another diagram may be added, and records the id so a
// page derived twice — a node reachable from two levels cannot happen, but a
// participant appearing in two sequences can — is built once.
func (b *builder) room(id string) bool {
	if b.seen[id] || len(b.out) >= b.limit {
		return false
	}
	b.seen[id] = true
	return true
}

func levelID(path string) string  { return "level:" + path }
func detailID(id string) string   { return "detail:" + id }
func sequenceID(id string) string { return "sequence:" + id }
func codemapID(id string) string  { return "codemap:" + id }

// level builds the diagram for one containment path and, recursively, for
// everything openable from it.
func (b *builder) level(path, parent, origin string) error {
	id := levelID(path)
	if !b.room(id) {
		return nil
	}

	children := b.childGroups(path)
	nodes := b.in.NodesIn(b.axis, path)

	g := core.New()
	g.Metadata = b.in.Metadata
	for _, a := range b.in.Axes {
		if a.ID == b.axis {
			g.Axes = append(g.Axes, a)
		}
	}

	// A container at this level becomes one box. It keeps the container's own
	// id, which is legal because ids are unique across nodes and groups
	// alike, and is what lets an edge that pointed at the container keep
	// pointing at the same thing here.
	for _, child := range children {
		grp, _ := b.in.Group(child)
		label := grp.Label
		if label == "" {
			label = grp.ID
		}
		g.Nodes = append(g.Nodes, core.Node{
			ID: grp.ID, Type: orDefault(grp.Type, "group"), Name: label,
			Attrs:  map[string]any{"container": true, "members": b.membersUnder(child)},
			Source: grp.Source, Claim: grp.Claim,
		})
	}
	for _, n := range nodes {
		copied := *n
		copied.Groups = nil
		g.Nodes = append(g.Nodes, copied)
	}

	// Every edge in the whole graph is lifted to this level: an end that sits
	// deeper is drawn as the container it is in. That is how a level says
	// "this namespace talks to that one" without drawing either one's
	// contents.
	at := b.representatives(path, children)
	g.Edges = liftEdges(b.in.Edges, at)
	carry(b.in, g)

	g.Normalize()
	if err := g.Validate(); err != nil {
		return fmt.Errorf("level %q: %w", path, err)
	}

	d := Diagram{
		ID: id, Kind: levelKind(len(children), len(nodes)), Graph: g,
		Title: b.levelTitle(path), Parent: parent, Origin: origin,
		Subtitle: fmt.Sprintf("%d containers · %d resources", len(children), len(nodes)),
	}
	for _, child := range children {
		grp, _ := b.in.Group(child)
		childPath := join(path, child)
		d.Opens = append(d.Opens, Opening{
			Element: child, Diagram: levelID(childPath),
			Kind:  levelKind(len(b.childGroups(childPath)), len(b.in.NodesIn(b.axis, childPath))),
			Label: orDefault(grp.Label, grp.ID),
		})
	}
	// Kept as they are decided, because the code maps below need the same
	// answers and asking again means walking every edge in the graph a second
	// time for every node of every level.
	var maps []Opening
	for _, n := range nodes {
		open, ok := b.detailOpening(n.ID)
		if !ok {
			continue
		}
		d.Opens = append(d.Opens, open)
		if open.Kind == KindCodemap {
			maps = append(maps, open)
		}
	}
	b.out = append(b.out, d)

	// Every code map of this level, before anything else is descended into.
	//
	// Pages are built in the order this walks, and the budget is spent in that
	// order. Both loops below spend it freely: nodes are walked in id order,
	// where every function of a repository sorts ahead of the repository
	// itself, and a child level descends as far as it can before returning. A
	// repository big enough to fill the budget with the pages of its own code
	// therefore left the container that runs it opening onto nothing — which
	// is the one descent this page exists to offer.
	//
	// The page only, not the pages of its members: descending into the first
	// repository's members is what used to exhaust the budget before the
	// second repository was reached, and one repository saved at the cost of
	// the next one is not the rule this is meant to be.
	for _, open := range maps {
		if err := b.codemapPage(open.Element, open); err != nil {
			return err
		}
	}
	for _, child := range children {
		if err := b.level(join(path, child), id, child); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		if err := b.detail(n.ID); err != nil {
			return err
		}
	}
	return nil
}

// detailOpening decides whether a node has an inside worth opening, and what
// to call the page behind it. A node nothing touches has none: the detail
// page would repeat the box the reader already clicked.
func (b *builder) detailOpening(id string) (Opening, bool) {
	// A repository somebody has placed opens its code rather than its
	// neighbours. The reader who got here clicked a container asking what it
	// runs, and the list of workloads that share the image is not that answer.
	//
	// One element has one inside, and the viewer keeps one door per box, so
	// this replaces the detail page rather than sitting beside it.
	//
	// Asked before the neighbours, because it does not depend on them. The
	// guard below is about a detail page having nothing on it that the box
	// already said; a code map is somewhere else entirely, and denying the
	// build record that joined this repository to a workload — which is what
	// suppressing one is for — left the repository holding code nobody could
	// reach, while the command line went on saying there was a map to open.
	subject, _ := b.node(id)
	if name, ok := repositoryNamed(subject); ok {
		if len(b.codeFor(name)) > 0 {
			// Named after the code it draws, not after the box it was opened
			// from. One repository can be here as a box per input — the same
			// repository, each box the one some workload was built from — and
			// they are all open onto the one input's code. A page apiece drew
			// that code once per box, under the same title, out of the same
			// budget; this way they are doors into one room.
			return Opening{Element: id, Diagram: codemapID(name), Kind: KindCodemap, Label: "コードマップ"}, true
		}
	}

	held, touched, called := b.around(id)
	if len(held) == 0 && len(touched) == 0 {
		return Opening{}, false
	}

	kind := KindDetail
	label := "中身"
	if len(held) == 0 && called > 0 {
		kind, label = KindCommunication, "呼び出し関係"
	}
	// A type is drawn as a class. It is the same page — one element has one
	// inside — read the way the thing itself is written: members listed in the
	// box, and a line to every other type the declaration mentions.
	//
	// A class page keeps less than a detail page does, so the question "is
	// there anything in there" has to be asked of what the class page will
	// actually show. A type reached only by things it is not related to —
	// a function that takes one, a file that contains it — has an empty class
	// diagram, and a door into an empty room is the thing this guard exists to
	// prevent.
	if subject != nil && subject.Type == codeType {
		declares, drawn := b.classOf(id, dedupe(sorted(append(append([]string{}, held...), touched...))))
		if len(declares) == 0 && len(drawn) == 0 {
			return Opening{}, false
		}
		kind, label = KindClass, "クラス図"
	}
	return Opening{Element: id, Diagram: detailID(id), Kind: kind, Label: label}, true
}

// codeFor is the code map of one repository: every input that says it is that
// repository's code, read together.
//
// Asked of the name rather than of the box, because the answer is not the
// box's. A repository is here as one box, as a box per input it was read
// beside, or as none, and where its code is does not change with that. The
// inputs say it — `metadata.inputs[].repository` — and every box of that name
// opens the same room.
func (b *builder) codeFor(name string) []string {
	if out, ok := b.byName[name]; ok {
		return out
	}
	var out []string
	if b.in.Metadata != nil {
		for _, in := range b.in.Metadata.Inputs {
			if in.Repository == name {
				out = append(out, b.codeOf(in.ID)...)
			}
		}
	}
	out = sorted(dedupe(out))
	if b.byName == nil {
		b.byName = map[string][]string{}
	}
	b.byName[name] = out
	return out
}

// scopesOf is the inputs whose code is this repository's: the ones somebody
// said the repository is. It is the same question codeFor asks, kept apart
// because what is behind a box is every node of those inputs and not only the
// ones a line put on the map.
func (b *builder) scopesOf(name string) map[string]bool {
	of := map[string]bool{}
	if name == "" || b.in.Metadata == nil {
		return of
	}
	for _, in := range b.in.Metadata.Inputs {
		// An input with no id stamps nothing, so it would stand for every
		// node that carries no stamp — which is the opposite of what a scope
		// is for.
		if in.ID != "" && in.Repository == name {
			of[in.ID] = true
		}
	}
	return of
}

// repositoryNamed is the name of the repository a node stands for, when it
// stands for one.
func repositoryNamed(n *core.Node) (string, bool) {
	if n == nil || n.Type != repositoryType || n.Name == "" {
		return "", false
	}
	return n.Name, true
}

// codeOf is CodeOf, read once per scope.
//
// Once, for the same reason declares is: detailOpening asks it of every
// repository node on every page it considers, and walking every edge and every
// node each time turns an estate into a quadratic one.
func (b *builder) codeOf(scope string) []string {
	if out, ok := b.code[scope]; ok {
		return out
	}
	out := CodeOf(b.in, scope)
	if b.code == nil {
		b.code = map[string][]string{}
	}
	b.code[scope] = out
	return out
}

// CodeOf is the code read from one input: the functions, the files that import
// something, and what they import.
//
// Every node of an input carries that input's id, which is what makes this a
// selection rather than a guess.
//
// Every box here is on a line. A file is on the page to carry the line out to
// what it uses, a package is there because something reached it, and a
// function is there because it calls or is called — the one that calls nothing
// and is called by nothing has nothing to say on a page about flow, and a
// thousand of them is a page nobody can read, which is the complaint the atlas
// exists to answer rather than to reproduce behind a container's box. A
// function nothing calls but which calls something is where a request comes
// in, so it stays.
//
// And so does one somebody said serves an operation, whether or not it is on
// any line the parser drew. A handler is reached from outside the tree, so the
// call graph cannot see that it is an entrance; the claim is what says so, and
// it is written down precisely because nothing here can read it. What the
// claim points at is not returned: an operation belongs to the document that
// declared it, not to this repository.
//
// Exported because the command line has to answer the same question before it
// accepts a mapping: an input this finds nothing in is an input whose box
// cannot be opened. Two readings of "has code" that differ is how a mapping
// passes every check and then draws nothing.
func CodeOf(g *core.Graph, scope string) []string {
	if scope == "" {
		return nil
	}
	kind := map[string]string{}
	// And the boxes a line may reach outside this repository, whatever input
	// they came from: the far end of a serves claim belongs to the document
	// that declared it, so that pairing cannot be asked of the scope alone.
	elsewhere := map[string]string{}
	for _, n := range g.Nodes {
		if farType(n.Type) {
			elsewhere[n.ID] = n.Type
		}
		if of, _ := n.Attrs["repository"].(string); of != scope {
			continue
		}
		switch n.Type {
		case codeFunction, codePackage, codeFile:
			kind[n.ID] = n.Type
		}
	}

	on := map[string]bool{}
	for _, e := range g.Edges {
		// A suppressed edge is one somebody said is not there, and every other
		// reading in this file skips it. Keeping it here put a box on the page
		// for a line a person had denied — and let a repository whose code
		// graph is denied in full answer "there is a code map to draw" at the
		// command line.
		if e.Suppressed {
			continue
		}
		// Both ends of the kind the line joins. The page keeps a line only
		// when both of its ends are on it, so an end that would not be chosen
		// takes the line with it — and the other end, chosen for a line that
		// is no longer there, sits on a page whose whole rule is that every
		// box is on one. A file importing something that is not a package is
		// not this project's own reading, but a graph is a document and
		// somebody else may write one.
		//
		// And only the near end is chosen when the far one is not ours. A
		// served operation is drawn on the map, but as what a line runs to:
		// counting it here would make this repository's code include a box
		// nobody read out of it, and the level would then have it lifted away.
		//
		// The function is chosen even if it calls nothing and nothing calls
		// it. That is the whole point of the claim: a handler reached from
		// outside the tree is where a request comes in, and the rules above
		// can only see the ones something inside the tree calls.
		i, ok := codeLine(e.Relation)
		if !ok || kind[e.From] != codeLines[i].from {
			continue
		}
		if !codeLines[i].ours {
			if elsewhere[e.To] == codeLines[i].to {
				on[e.From] = true
			}
			continue
		}
		if kind[e.To] == codeLines[i].to {
			on[e.From], on[e.To] = true, true
		}
	}

	out := make([]string, 0, len(on))
	for id := range on {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// codemap builds one repository's code as a map: what calls what, what it
// imports, and which of its functions somebody said answer an API operation.
//
// What is drawn is what the parser recorded, plus what somebody signed for. A
// function nothing in the tree calls is where a request can come in, the calls
// are what happens next, and an imported package is a way out. One line
// crosses to the estate around the box: an operation appears beside the
// function a serves claim says answers it, because neither reader can make
// that join on its own and somebody wrote it down — see docs/api.md. Which
// import carries which outbound call is still written down by nothing; see
// docs/code.md for what the reading refuses.
func (b *builder) codemap(id string, open Opening) error {
	if err := b.codemapPage(id, open); err != nil {
		return err
	}
	// Once. This is reached from the level and again from the detail page of
	// every workload the record joined to this repository, and every visit
	// asked detailOpening of every member — which walks every edge in the
	// graph. The pages are already there after the first pass; detail would
	// decline each of them and charge the walk for saying so.
	if b.descended[open.Diagram] {
		return nil
	}
	// And not at all once the budget is gone. Every member would be asked
	// whether it has an inside — a walk of every edge apiece — for pages none
	// of which can be made, and the estate that spends the budget is exactly
	// the one where there are most members to ask.
	if len(b.out) >= b.limit {
		return nil
	}
	subject, _ := b.node(id)
	name, ok := repositoryNamed(subject)
	if !ok {
		return nil
	}
	if b.descended == nil {
		b.descended = map[string]bool{}
	}
	b.descended[open.Diagram] = true
	for _, member := range b.codeFor(name) {
		if err := b.detail(member); err != nil {
			return err
		}
	}
	return nil
}

// trailUpFrom is where a code map's "back" goes: the level the boxes that open
// it are on. One repository can be here as a box per input, and where they
// disagree the answer is the level they are all under, which is true of all of
// them — a trail leading back to a level the reader was never on is worse than
// a longer one.
func (b *builder) trailUpFrom(name string) string {
	parent := ""
	for i := range b.in.Nodes {
		n := &b.in.Nodes[i]
		if of, ok := repositoryNamed(n); !ok || of != name {
			continue
		}
		level := b.levelOf(n.ID)
		if parent == "" {
			parent = level
			continue
		}
		if parent != level {
			return levelID("")
		}
	}
	if parent == "" {
		return levelID("")
	}
	return parent
}

// joins reports whether a line is one a code map draws: the ends of the kinds
// that relation holds between.
func joins(b *builder, e core.Edge) bool {
	from, _ := b.node(e.From)
	to, _ := b.node(e.To)
	if from == nil || to == nil {
		return false
	}
	i, ok := codeLine(e.Relation)
	return ok && from.Type == codeLines[i].from && to.Type == codeLines[i].to
}

// codemapPage builds the map itself and nothing under it.
//
// Separate from the descent into its members because the two want opposite
// things from the budget: every map of a level is made before any of them
// spends what is left on the pages of its own code. One repository saved at
// the cost of the next one is not the rule this is meant to be.
func (b *builder) codemapPage(id string, open Opening) error {
	subject, _ := b.node(id)
	name, ok := repositoryNamed(subject)
	if !ok {
		return nil
	}
	members := b.codeFor(name)
	if len(members) == 0 || !b.room(open.Diagram) {
		return nil
	}

	g := core.New()
	g.Metadata = b.in.Metadata
	present := map[string]bool{}
	for _, member := range members {
		n, ok := b.node(member)
		if !ok {
			continue
		}
		place(g, n)
		present[member] = true
	}
	var lines []core.Edge
	for _, e := range b.in.Edges {
		// Folded, the way every other relation in this file is read. A graph
		// that writes `Imports` loses the lines here and the files with them,
		// leaving a page of boxes and no flow, and saying nothing about it.
		if _, ok := codeLine(e.Relation); !ok {
			continue
		}
		// And of the kind that line joins, the same pairing CodeOf asked for
		// when it chose the boxes. Drawing a line the choosing refused is the
		// page contradicting the rule it was built by.
		if !joins(b, e) {
			continue
		}
		lines = append(lines, e)
	}

	// The operations the code on this page answers. They are not this
	// repository's code and CodeOf did not choose them, so they arrive here as
	// what the lines run to rather than as members: the map crosses into the
	// estate exactly as far as somebody wrote down that it does.
	//
	// Not on a suppressed one. A denied claim is drawn when both of its ends
	// are already here — the rule every other line on this page follows — but
	// it never brings a box with it, because a box on the page because of a
	// line somebody denied is the page arguing with itself.
	for _, e := range lines {
		// Which end is the far one is the table's to say, not this loop's. A
		// second line that crosses — a gRPC service, a router's registration —
		// would otherwise be chosen by CodeOf and drawn by joins and have no
		// box at this end, and the page would drop it for want of one.
		i, ok := codeLine(e.Relation)
		if !ok || codeLines[i].ours || e.Suppressed || !present[e.From] || present[e.To] {
			continue
		}
		n, ok := b.node(e.To)
		if !ok {
			continue
		}
		place(g, n)
		present[e.To] = true
	}

	for _, e := range lines {
		// Suppressed lines are drawn, the way every other page draws them. A
		// denial is a thing somebody said, and the reader decides whether to
		// see it — dropping it here made --hide-suppressed a no-op on this
		// page and left no way to learn that the call was denied at all.
		//
		// No box is ever here *because* of one: CodeOf leaves those lines out
		// of the choosing, so a suppressed line only ever joins two boxes that
		// a line nobody denied had already put on the page.
		if present[e.From] && present[e.To] {
			g.Edges = append(g.Edges, e)
		}
	}
	carry(b.in, g)
	g.Normalize()
	if err := g.Validate(); err != nil {
		return fmt.Errorf("code map %q: %w", id, err)
	}

	// Named for every box that opens it, not for whichever one got here
	// first. The page is one room with a door per box, so the box the reader
	// happened to come through is not what it is a page of: two repositories
	// of a monorepo share an input, and titling it after the first meant
	// clicking the second one's box arrived at a page named after the other.
	// The trail back up is the same question — a box in another namespace led
	// back to a level the reader had never been on — and where the boxes
	// disagree the answer is the level they are all under.
	d := Diagram{
		ID: open.Diagram, Kind: KindCodemap, Graph: g,
		Parent:   b.trailUpFrom(name),
		Origin:   id,
		Title:    name,
		Subtitle: open.Label,
	}
	// A box on this page opens the same way it opens anywhere else: a function
	// its own page, a package or a file its contents. The descent is what the
	// page is for — a reader who arrived from a running container is on their
	// way to something smaller — and a page whose boxes open nothing is a dead
	// end at the exact point the trail was supposed to keep going.
	//
	// A type is not a box here, so no door on this page is a class diagram.
	// This page is about flow, and a declaration takes part in flow only
	// through the functions that use it. A type is declared in a file, so the
	// class diagram is reached through the file's page rather than through the
	// code that uses the type.
	//
	// Asked of every member, however many there are. Stopping early was tried
	// and was wrong: the descent walks the members too, so the pages past the
	// cut were built and then had no door — reachable from nothing, which is
	// worse than the question is expensive. The question is what got cheaper
	// instead; see around.
	//
	// The members. An operation drawn here gets no door, and the reason is
	// what is behind one: its detail page is its neighbours, and its only
	// neighbour is the function on this page that serves it. The door led to
	// a page strictly smaller than the one the reader was already on, and
	// charged the atlas a page for it — which under a budget is a page of
	// this repository's code not drawn. The estate draws the operation on its
	// level, among the things it actually sits with; this box says which code
	// answers it, and that is the whole of what the map knows.
	for _, member := range members {
		if open, ok := b.detailOpening(member); ok {
			d.Opens = append(d.Opens, open)
		}
	}
	b.out = append(b.out, d)
	return nil
}

// place copies a node onto a page.
//
// Without its group path: the page is not the estate the node was grouped in,
// and a path naming a container that is not here fails validation.
//
// With its own attrs and its own claim rather than the graph's. A node is
// copied by value and those are pointers, so a page that adjusted one would be
// editing the graph it was derived from and every other page derived from it —
// which is what trimSinks says about coverage, three hundred lines down, for
// the same reason. An operation arrives here carrying whoever's claim said it
// is served.
func place(g *core.Graph, n *core.Node) {
	copied := *n
	copied.Groups = nil
	copied.Attrs = cloneAttrs(n.Attrs)
	if n.Claim != nil {
		claim := *n.Claim
		copied.Claim = &claim
	}
	g.Nodes = append(g.Nodes, copied)
}

// levelOf is the page an element belongs under: the level of the container it
// sits in.
//
// A detail page's parent is that level and never the page a reader happened to
// arrive from. Both are true statements about how the page was reached, and
// only one of them is stable — deriving the same estate twice, or reaching the
// same element from two neighbours, would otherwise produce two different
// trails back up, and the trail is the one thing a reader who has descended
// four times is relying on.
func (b *builder) levelOf(id string) string {
	n, ok := b.node(id)
	if !ok {
		return levelID("")
	}
	return levelID(n.Groups[b.axis])
}

// detail builds one element's page: what it holds, and what it talks to.
func (b *builder) detail(id string) error {
	open, ok := b.detailOpening(id)
	if !ok {
		return nil
	}
	if open.Kind == KindCodemap {
		return b.codemap(id, open)
	}
	if !b.room(open.Diagram) {
		return nil
	}
	subject, ok := b.node(id)
	if !ok {
		return nil
	}
	held, touched, _ := b.around(id)

	g := core.New()
	g.Metadata = b.in.Metadata
	centre := *subject
	centre.Groups = nil
	g.Nodes = append(g.Nodes, centre)

	members := dedupe(sorted(append(append([]string{}, held...), touched...)))

	// A class lists what it declares and draws what it relates to.
	//
	// UML puts members inside the box, and it is right to: a class with nine
	// methods drawn as nine boxes is a picture of nine things, when it is a
	// picture of one thing with nine methods. Nothing is lost by listing them
	// — the file that contains the type contains its functions too, and that
	// page still draws every one of them as a box a reader can open.
	if centre.Type == codeType {
		declares, drawn := b.classOf(id, members)
		members = drawn
		if len(declares) > 0 {
			if centre.Attrs == nil {
				centre.Attrs = map[string]any{}
			} else {
				centre.Attrs = cloneAttrs(centre.Attrs)
			}
			centre.Attrs["declares"] = declares
		}
		g.Nodes[0] = centre
	}

	for _, other := range members {
		n, ok := b.node(other)
		if !ok {
			continue
		}
		copied := *n
		copied.Groups = nil
		if copied.Attrs == nil {
			copied.Attrs = map[string]any{}
		} else {
			copied.Attrs = cloneAttrs(copied.Attrs)
		}
		// inside is the difference between "this is in the box you opened"
		// and "this is something the box talks to". Both belong on the page —
		// the reader asked what the box is — and drawing them the same way
		// would say the wrong one is contained.
		copied.Attrs["inside"] = contains(held, other)
		g.Nodes = append(g.Nodes, copied)
	}

	// Only edges with an end on the subject. A page about one element that
	// also draws its neighbours' relationships to each other is a small
	// architecture diagram, and the reader already has one of those.
	present := map[string]bool{}
	for _, n := range g.Nodes {
		present[n.ID] = true
	}
	for _, e := range b.in.Edges {
		if e.From != id && e.To != id {
			continue
		}
		if present[e.From] && present[e.To] {
			g.Edges = append(g.Edges, e)
		}
	}
	carry(b.in, g)
	g.Normalize()
	if err := g.Validate(); err != nil {
		return fmt.Errorf("detail %q: %w", id, err)
	}

	d := Diagram{
		ID: open.Diagram, Kind: open.Kind, Graph: g,
		Title: orDefault(subject.Name, subject.ID), Subtitle: subject.Type,
		Parent: b.levelOf(id), Origin: id,
	}
	for _, other := range members {
		if nested, ok := b.detailOpening(other); ok {
			d.Opens = append(d.Opens, nested)
		}
	}
	if seq, ok := b.sequenceOpening(id); ok {
		d.Opens = append(d.Opens, seq)
	}
	b.out = append(b.out, d)

	if err := b.sequence(id, open.Diagram); err != nil {
		return err
	}
	for _, other := range members {
		if err := b.detail(other); err != nil {
			return err
		}
	}
	return nil
}

// readDeclarations reads the declares edges out of the document, once.
func (b *builder) readDeclarations() {
	b.declares = map[string]map[string]bool{}
	for _, e := range b.in.Edges {
		if e.Suppressed || !strings.EqualFold(e.Relation, relDeclares) {
			continue
		}
		if b.declares[e.From] == nil {
			b.declares[e.From] = map[string]bool{}
		}
		b.declares[e.From][e.To] = true
	}
}

// classOf splits what is around a type into what it declares and what it is
// drawn beside: the member list in the box, and the other types on the page.
func (b *builder) classOf(id string, around []string) (declares, drawn []string) {
	centre, ok := b.node(id)
	if !ok {
		return nil, nil
	}
	for _, other := range around {
		n, ok := b.node(other)
		if !ok {
			continue
		}
		if n.Type == codeFunction && b.declares[id][other] {
			// Inside the class the receiver is the box it is written in, so
			// "Rule.check" is "check". The function keeps its full name
			// everywhere else, where it needs to say whose it is.
			declares = append(declares,
				strings.TrimPrefix(orDefault(n.Name, n.ID), centre.Name+"."))
			continue
		}
		if n.Type == codeType {
			drawn = append(drawn, other)
		}
	}
	return declares, drawn
}

// sorted is sort.Strings with a value to hand back, for the places where the
// list is built and used in one expression.
func sorted(in []string) []string {
	sort.Strings(in)
	return in
}

func (b *builder) sequenceOpening(id string) (Opening, bool) {
	if steps, _ := b.chainFrom(id); len(steps) == 0 {
		return Opening{}, false
	}
	return Opening{Element: id, Diagram: sequenceID(id), Kind: KindSequence, Label: "呼び出し順"}, true
}

// chainFrom is the order a sequence page draws, and whether anything observed
// it.
//
// A recorded route beats a walk this package worked out. The walk is a reading
// of edges — A calls B, B calls C, so a request probably goes A, B, C — and a
// path is a claim that a request *went* A, B, C. Where both exist the second
// is the better answer, and the page says which one it is drawing rather than
// presenting them as the same thing.
//
// The longest route starting here wins, because a route that goes further
// tells the reader more and the shorter ones are usually its beginning. Ties
// go to whichever sorts first, so the same graph draws the same sequence.
func (b *builder) chainFrom(id string) ([]core.Edge, bool) {
	if walked := b.recorded(id); len(walked) > 0 {
		return walked, true
	}
	return b.callChain(id), false
}

// recorded turns the best route starting at id into steps.
func (b *builder) recorded(id string) []core.Edge {
	var best *core.Path
	for i := range b.in.Paths {
		p := &b.in.Paths[i]
		// Only what something walked. A declared route is the same kind of
		// reading the walk already is, and preferring it would say "observed"
		// about an order nobody saw.
		if p.Kind != core.EdgeObserved || len(p.Nodes) < 2 || p.Nodes[0] != id {
			continue
		}
		if best == nil || len(p.Nodes) > len(best.Nodes) ||
			(len(p.Nodes) == len(best.Nodes) && p.Key() < best.Key()) {
			best = p
		}
	}
	if best == nil {
		return nil
	}

	steps := make([]core.Edge, 0, len(best.Nodes)-1)
	for i := 1; i < len(best.Nodes); i++ {
		from, to := best.Nodes[i-1], best.Nodes[i]
		if _, ok := b.node(from); !ok {
			return nil
		}
		if _, ok := b.node(to); !ok {
			return nil
		}
		steps = append(steps, b.messageFor(from, to, best))
	}
	return steps
}

// messageFor is the edge a step is drawn from: the one somebody claimed when
// there is one, and otherwise the route's own claim.
//
// The second case is not an invented relationship. A route that says a request
// went from here to there is already a claim that it went, and drawing it
// without an edge underneath would be dropping evidence the document has.
func (b *builder) messageFor(from, to string, p *core.Path) core.Edge {
	var fallback *core.Edge
	for i := range b.in.Edges {
		e := &b.in.Edges[i]
		if e.From != from || e.To != to || e.Suppressed {
			continue
		}
		if e.Kind == core.EdgeObserved {
			return *e
		}
		if fallback == nil {
			fallback = e
		}
	}
	if fallback != nil {
		return *fallback
	}
	return core.Edge{From: from, To: to, Kind: p.Kind, Relation: "calls", Claim: p.Claim}
}

// sequence builds the call chain that starts at one element.
//
// Where the order comes from is chainFrom's decision, and the page carries the
// answer: a route something walked when the document records one, and
// otherwise a depth-first walk of the declared references in a stable order.
//
// The second is a reading, not an observation. A static graph records that A
// calls B and that B calls C; it does not record that A called B before it
// called C, and the page says "derived" rather than pretending otherwise.
func (b *builder) sequence(id, parent string) error {
	steps, observed := b.chainFrom(id)
	if len(steps) == 0 {
		return nil
	}
	sid := sequenceID(id)
	if !b.room(sid) {
		return nil
	}
	subject, ok := b.node(id)
	if !ok {
		return nil
	}

	g := core.New()
	g.Metadata = b.in.Metadata
	participants := []string{id}
	for _, s := range steps {
		participants = append(participants, s.From, s.To)
	}
	for _, p := range dedupeStable(participants) {
		n, ok := b.node(p)
		if !ok {
			continue
		}
		copied := *n
		copied.Groups = nil
		g.Nodes = append(g.Nodes, copied)
	}
	for i, s := range steps {
		e := s
		e.Attrs = cloneAttrs(e.Attrs)
		if e.Attrs == nil {
			e.Attrs = map[string]any{}
		}
		// The step number is what makes this a sequence rather than a
		// picture of the same edges. It is an attribute so that the edge
		// remains the edge somebody claimed, carrying its own kind and claim
		// into a diagram that did not invent it.
		e.Attrs["step"] = i + 1
		g.Edges = append(g.Edges, e)
	}
	carry(b.in, g)
	// Normalize merges edges that agree on ends, kind and relation, which
	// would fold two steps of a chain that visits the same pair twice into
	// one. A sequence is the one diagram where that is wrong, so its edges
	// are validated but left in the order the walk produced them.
	if err := g.Validate(); err != nil {
		return fmt.Errorf("sequence %q: %w", id, err)
	}

	order, said := OrderDerived, "導出された順序"
	if observed {
		order, said = OrderObserved, "観測された順序"
	}
	d := Diagram{
		ID: sid, Kind: KindSequence, Graph: g, Order: order,
		Title:    orDefault(subject.Name, subject.ID) + " から",
		Subtitle: fmt.Sprintf("%d steps · %s", len(steps), said),
		Parent:   parent, Origin: id,
	}
	for _, n := range g.Nodes {
		if n.ID == id {
			continue
		}
		if open, ok := b.detailOpening(n.ID); ok {
			d.Opens = append(d.Opens, open)
		}
	}
	b.out = append(b.out, d)
	return nil
}

// callChain walks call relations depth first from a root, in a stable order,
// visiting each edge at most once.
func (b *builder) callChain(root string) []core.Edge {
	var steps []core.Edge
	walked := map[string]bool{}

	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if depth >= b.depth {
			return
		}
		var out []core.Edge
		best := map[string]core.Edge{}
		var order []string
		for _, e := range b.in.Edges {
			if e.From != id || e.Suppressed || !isCall(e) {
				continue
			}
			// Either end of an edge may be a container, and a sequence draws
			// participants, which are nodes. Following one would put a
			// message on a lifeline the diagram does not have — and because
			// the projected graph is validated, that is not a wrong picture
			// but no picture at all: the error travels all the way out and
			// the render produces nothing.
			if _, ok := b.node(e.To); !ok {
				continue
			}
			// One call, however many kinds of evidence found it. A
			// reference the configuration declares and a trace of the same
			// call are two claims about one thing, and a sequence that
			// numbered them separately would say the request went to the
			// ledger twice. Which of them is drawn is settled below, and it
			// is the one that saw it happen.
			key := e.From + "\x00" + e.To + "\x00" + e.Relation
			if walked[key] {
				continue
			}
			if at, seen := best[key]; !seen || better(e, at) {
				if !seen {
					order = append(order, key)
				}
				best[key] = e
			}
		}
		out = out[:0]
		sort.Strings(order)
		for _, key := range order {
			out = append(out, best[key])
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].To != out[j].To {
				return out[i].To < out[j].To
			}
			return out[i].Relation < out[j].Relation
		})
		for _, e := range out {
			key := e.From + "\x00" + e.To + "\x00" + e.Relation
			if walked[key] {
				continue
			}
			walked[key] = true
			steps = append(steps, e)
			walk(e.To, depth+1)
		}
	}
	walk(root, 0)
	return steps
}

// better reports whether one claim about a call is the one to draw. Something
// that saw the call happen beats something that says it could.
func better(a, b core.Edge) bool {
	if (a.Kind == core.EdgeObserved) != (b.Kind == core.EdgeObserved) {
		return a.Kind == core.EdgeObserved
	}
	return false
}

// around splits what an element is joined to into what it holds and what it
// merely touches, and counts the calls among the latter.
func (b *builder) around(id string) (held, touched []string, calls int) {
	heldSet := map[string]bool{}
	touchedSet := map[string]bool{}
	for _, i := range b.touching(id) {
		e := b.in.Edges[i]
		if e.Suppressed {
			continue
		}
		var other string
		switch {
		case e.From == id:
			other = e.To
		case e.To == id:
			other = e.From
		default:
			continue
		}
		if _, ok := b.node(other); !ok {
			continue
		}
		if other == id {
			continue
		}
		if isCall(e) {
			calls++
		}
		if holdsFrom(e, id) {
			heldSet[other] = true
			continue
		}
		touchedSet[other] = true
	}
	for other := range heldSet {
		delete(touchedSet, other)
	}
	return sortedKeys(heldSet), sortedKeys(touchedSet), calls
}

// holdsFrom reports whether this edge says the far end is inside id.
func holdsFrom(e core.Edge, id string) bool {
	r := strings.ToLower(e.Relation)
	if !holdRelations[r] && !reversedHolds[r] {
		return false
	}
	if reversedHolds[r] {
		// Recorded child to parent, so id holds the other end only when id is
		// the one being pointed at.
		return e.To == id
	}
	return e.From == id
}

func isCall(e core.Edge) bool {
	r := strings.ToLower(e.Relation)
	if r == "" {
		return e.Kind == core.EdgeObserved
	}
	return matchesAny(r, callRelations)
}

// representatives maps every node id to what stands for it at one level: the
// node itself when it sits there, and otherwise the child container it is
// somewhere inside. A node in a different branch has no representative and
// its edges are not drawn here.
func (b *builder) representatives(path string, children []string) map[string]string {
	at := map[string]string{}
	for i := range b.in.Nodes {
		n := &b.in.Nodes[i]
		np := n.Groups[b.axis]
		if np == path {
			at[n.ID] = n.ID
			continue
		}
		if child := childOnPath(path, np); child != "" && contains(children, child) {
			at[n.ID] = child
		}
	}
	// An edge may point at a container rather than a node. It is drawn here
	// when that container is at this level or under one that is.
	for _, grp := range b.in.Groups {
		if grp.Axis != b.axis {
			continue
		}
		gp, err := b.in.GroupPath(grp.ID)
		if err != nil {
			continue
		}
		if gp == path {
			continue
		}
		if child := childOnPath(path, gp); child != "" && contains(children, child) {
			at[grp.ID] = child
		}
	}
	for _, child := range children {
		at[child] = child
	}
	return at
}

// childOnPath returns the segment of nodePath that is a direct child of path,
// or "" when nodePath is not under path at all.
func childOnPath(path, nodePath string) string {
	if nodePath == "" || nodePath == path {
		return ""
	}
	rest := nodePath
	if path != "" {
		prefix := path + core.GroupSeparator
		if !strings.HasPrefix(nodePath, prefix) {
			return ""
		}
		rest = strings.TrimPrefix(nodePath, prefix)
	}
	return strings.SplitN(rest, core.GroupSeparator, 2)[0]
}

// liftEdges rewrites every edge onto the representatives of its ends, drops
// what cannot be placed, and folds what lands on the same pair into one line
// carrying how many references it stands for.
//
// # Suppression is counted, not folded
//
// A reference somebody asserted is not real and a reference that is are two
// different facts, and a pair of containers can easily have both. They cannot
// become two lines: at this level they have the same ends, kind and relation,
// which is one edge identity, and core.Normalize would merge them again on
// the fail-safe terms it is right to apply to two claims about one edge — so
// the real reference would arrive drawn as denied.
//
// So the line is the real references, and the denied ones travel as a count
// beside them. A pair whose references were *all* denied still gets its line,
// drawn as denied, because "somebody said this is wrong" and "this never
// existed" are different facts and only the first one is true.
func liftEdges(in []core.Edge, at map[string]string) []core.Edge {
	type key struct {
		from, to string
		kind     core.EdgeKind
		relation string
	}
	order := []key{}
	merged := map[key]*core.Edge{}
	counts := map[key]int{}
	denied := map[key]int{}

	for _, e := range in {
		from, okFrom := at[e.From]
		to, okTo := at[e.To]
		if !okFrom || !okTo || from == to {
			continue
		}
		k := key{from, to, e.Kind, e.Relation}
		if e.Suppressed {
			denied[k]++
		} else {
			counts[k]++
		}

		// The line stands for the references that were not denied, so one of
		// those is what it is built from. A denied reference is only the
		// representative while nothing else has been seen for this pair, and
		// is replaced by the first real one that arrives.
		standing := merged[k]

		// One line standing for several is here for no reason but a denial
		// only if every reference under it is. Copying the flag off whichever
		// reference happened to represent the group would put "nothing drew
		// this" on a line a parser drew.
		// Both flags are properties of the group, not of whichever reference
		// represents it: if one of them was drawn, something drew it, and if
		// one of them is a reader's word the relation is not one author's
		// sentence. Folded the way Normalize folds them over duplicates.
		absent := e.AssertedAbsent
		asserted := e.RelationAsserted
		if standing != nil {
			absent = absent && standing.AssertedAbsent
			asserted = asserted && standing.RelationAsserted
			// And the sentence the denial wrote about nothing having drawn
			// it becomes false. The sentence alone: taking the whole claim
			// from the side that drew the line loses the denier, because a
			// parser's reference carries no claim at all.
			if standing.AssertedAbsent && !e.AssertedAbsent {
				core.WithdrawDeniedNote(standing)
			}
			standing.AssertedAbsent, standing.RelationAsserted = absent, asserted
			if !standing.Suppressed || e.Suppressed {
				continue
			}
		}
		lifted := e
		lifted.From, lifted.To = from, to
		lifted.Attrs = cloneAttrs(e.Attrs)
		// Before the flag goes, not after: the sentence is withdrawn from a
		// claim that is still on an invented line, which is how the helper
		// knows there is one to withdraw.
		if absent != e.AssertedAbsent {
			core.WithdrawDeniedNote(&lifted)
		}
		lifted.AssertedAbsent, lifted.RelationAsserted = absent, asserted
		if standing == nil {
			order = append(order, k)
		}
		merged[k] = &lifted
	}

	out := make([]core.Edge, 0, len(order))
	for _, k := range order {
		e := *merged[k]
		if counts[k] > 1 || denied[k] > 0 {
			if e.Attrs == nil {
				e.Attrs = map[string]any{}
			}
			if counts[k] > 1 {
				e.Attrs["references"] = counts[k]
			}
			if denied[k] > 0 {
				e.Attrs["suppressed_references"] = denied[k]
			}
		}
		out = append(out, e)
	}
	return out
}

// carry copies the evidence attached to whatever survived a projection.
//
// A page that dropped it would not merely be missing a detail: the viewer
// decides a contested box's stroke, an abnormal reading's red, the label
// filters and the timeline from these three arrays, so a projection that
// leaves them behind draws an estate where nothing is contested and nothing
// is wrong.
//
// Evidence about something this page does not draw is left behind, because a
// standalone graph document carrying a subject it has no box for is a
// dangling reference rather than useful provenance. On a level that means a
// measurement on a member folded into its container is not shown on the
// container: rolling it up would be this package inventing a reading nobody
// took.
func carry(in, out *core.Graph) {
	present := make(map[string]bool, len(out.Nodes))
	for _, n := range out.Nodes {
		present[n.ID] = true
	}
	for _, o := range in.Observations {
		if present[o.Subject] {
			out.Observations = append(out.Observations, o)
		}
	}
	for _, r := range in.LogRecords {
		if r.Source == "" || present[r.Source] {
			out.LogRecords = append(out.LogRecords, r)
		}
	}
	out.LogStatus = in.LogStatus
	out.Conflicts = append(out.Conflicts, in.Conflicts...)
	filterConflicts(out)
	trimSinks(out, present)
}

// trimSinks drops the pointer from a coverage finding to a log destination
// this page has no box for.
//
// The finding stays. "Somebody looked and found no destination" is about the
// node it is on, and it is still true on a page that draws that node and not
// the bucket next to it — but the id would be a dangling reference, and
// core.Validate rejects the whole document for one, which on this path means
// the render produces nothing rather than a page missing a link.
//
// The coverage is copied before it is changed. Nodes are copied by value and
// their coverage is a pointer, so trimming in place would edit the graph this
// projection was derived from, and every other page derived from it.
func trimSinks(out *core.Graph, present map[string]bool) {
	for i := range out.Nodes {
		cov := out.Nodes[i].Coverage
		if cov == nil {
			continue
		}
		dangling := false
		for _, e := range cov.Evidence {
			if e.Sink != "" && !present[e.Sink] {
				dangling = true
				break
			}
		}
		if !dangling {
			continue
		}
		trimmed := *cov
		trimmed.Evidence = make([]core.Evidence, len(cov.Evidence))
		copy(trimmed.Evidence, cov.Evidence)
		for j := range trimmed.Evidence {
			if trimmed.Evidence[j].Sink != "" && !present[trimmed.Evidence[j].Sink] {
				trimmed.Evidence[j].Sink = ""
			}
		}
		out.Nodes[i].Coverage = &trimmed
	}
}

func (b *builder) childGroups(path string) []string {
	parent := ""
	if path != "" {
		parts := strings.Split(path, core.GroupSeparator)
		parent = parts[len(parts)-1]
	}
	return b.in.Children(b.axis, parent)
}

func (b *builder) membersUnder(child string) int {
	path, err := b.in.GroupPath(child)
	if err != nil {
		return 0
	}
	prefix := path + core.GroupSeparator
	n := 0
	for i := range b.in.Nodes {
		np := b.in.Nodes[i].Groups[b.axis]
		if np == path || strings.HasPrefix(np, prefix) {
			n++
		}
	}
	return n
}

func (b *builder) levelTitle(path string) string {
	if path == "" {
		if b.in.Metadata != nil && b.in.Metadata.Scope != "" {
			return b.in.Metadata.Scope
		}
		return "all"
	}
	parts := strings.Split(path, core.GroupSeparator)
	last := parts[len(parts)-1]
	if grp, ok := b.in.Group(last); ok {
		return orDefault(grp.Label, grp.ID)
	}
	return last
}

func levelKind(containers, nodes int) Kind {
	if containers > 0 && containers >= nodes {
		return KindPackage
	}
	return KindArchitecture
}

func join(path, child string) string {
	if path == "" {
		return child
	}
	return path + core.GroupSeparator + child
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func matchesAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func cloneAttrs(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func dedupeStable(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
