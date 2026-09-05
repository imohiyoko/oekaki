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

	// KindSequence is one call chain in order. The order is derived, and
	// says so; see sequenceFrom.
	KindSequence Kind = "sequence"
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
	depth := opts.Depth
	if depth <= 0 {
		depth = defaultSequenceDepth
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultAtlasLimit
	}

	b := &builder{in: in, axis: axis, depth: depth, limit: limit, seen: map[string]bool{}}
	if err := b.level("", "", ""); err != nil {
		return nil, err
	}
	b.prune()
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
	for _, n := range nodes {
		if open, ok := b.detailOpening(n.ID); ok {
			d.Opens = append(d.Opens, open)
		}
	}
	b.out = append(b.out, d)

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
	held, touched, called := b.around(id)
	if len(held) == 0 && len(touched) == 0 {
		return Opening{}, false
	}
	kind := KindDetail
	label := "中身"
	if len(held) == 0 && called > 0 {
		kind, label = KindCommunication, "呼び出し関係"
	}
	return Opening{Element: id, Diagram: detailID(id), Kind: kind, Label: label}, true
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
	n, ok := b.in.Node(id)
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
	if !b.room(open.Diagram) {
		return nil
	}
	subject, ok := b.in.Node(id)
	if !ok {
		return nil
	}
	held, touched, _ := b.around(id)

	g := core.New()
	g.Metadata = b.in.Metadata
	centre := *subject
	centre.Groups = nil
	g.Nodes = append(g.Nodes, centre)

	members := append(append([]string{}, held...), touched...)
	sort.Strings(members)
	for _, other := range dedupe(members) {
		n, ok := b.in.Node(other)
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
	for _, other := range dedupe(members) {
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
	for _, other := range dedupe(members) {
		if err := b.detail(other); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) sequenceOpening(id string) (Opening, bool) {
	if len(b.callChain(id)) == 0 {
		return Opening{}, false
	}
	return Opening{Element: id, Diagram: sequenceID(id), Kind: KindSequence, Label: "呼び出し順"}, true
}

// sequence builds the call chain that starts at one element.
//
// The order is derived, not observed. A static graph records that A calls B
// and that B calls C; it does not record that A called B before it called C,
// and nothing here pretends otherwise — the step numbers are a depth-first
// walk in a stable order, which is how a reader reads a call chain when
// nobody has traced one. An observed ordering, when traces provide one, is a
// different claim and belongs on the edges rather than in this walk.
func (b *builder) sequence(id, parent string) error {
	steps := b.callChain(id)
	if len(steps) == 0 {
		return nil
	}
	sid := sequenceID(id)
	if !b.room(sid) {
		return nil
	}
	subject, ok := b.in.Node(id)
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
		n, ok := b.in.Node(p)
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

	d := Diagram{
		ID: sid, Kind: KindSequence, Graph: g,
		Title:    orDefault(subject.Name, subject.ID) + " から",
		Subtitle: fmt.Sprintf("%d steps · 導出された順序", len(steps)),
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
			if _, ok := b.in.Node(e.To); !ok {
				continue
			}
			key := e.From + "\x00" + e.To + "\x00" + string(e.Kind) + "\x00" + e.Relation
			if walked[key] {
				continue
			}
			out = append(out, e)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].To != out[j].To {
				return out[i].To < out[j].To
			}
			return out[i].Relation < out[j].Relation
		})
		for _, e := range out {
			key := e.From + "\x00" + e.To + "\x00" + string(e.Kind) + "\x00" + e.Relation
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

// around splits what an element is joined to into what it holds and what it
// merely touches, and counts the calls among the latter.
func (b *builder) around(id string) (held, touched []string, calls int) {
	heldSet := map[string]bool{}
	touchedSet := map[string]bool{}
	for _, e := range b.in.Edges {
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
		if _, ok := b.in.Node(other); !ok {
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
		if standing != nil && (!standing.Suppressed || e.Suppressed) {
			continue
		}
		lifted := e
		lifted.From, lifted.To = from, to
		lifted.Attrs = cloneAttrs(e.Attrs)
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
