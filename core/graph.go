// Package core defines oekaki's intermediate representation and the
// operations that every parser and renderer shares.
//
// The IR is the actual product of this project. Parsers write it, enrichers
// annotate it, renderers read it, and none of them need to know about each
// other. Its JSON encoding is specified by schema/graph.schema.json.
package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Version is the IR schema version this package reads and writes.
const Version = "0.6"

// The versions Decode still reads. Each is validated against the frozen
// contract it was written to before it is migrated, so a document that was
// invalid then does not become valid by being read now.
//
// 0.4 carries untyped conflict targets, which are resolved against the graph.
// 0.5 differs only by not having paths, so reading one is a change of version
// string and nothing else — but it is still listed rather than waved through,
// because "shaped like the current version" and "declared as it" are different
// claims and only the second one is checked.
const (
	legacyV04 = "0.4"
	legacyV05 = "0.5"
)

// GroupSeparator joins group ids into the paths stored on Node.Groups.
const GroupSeparator = "/"

// AxisNetwork is the axis renderers nest by unless told otherwise: the
// containment the infrastructure itself imposes, such as subnets inside a VPC.
const AxisNetwork = "network"

// AxisProvider groups by the provider a resource belongs to. In an estate that
// mixes on-premises and several clouds, this is often the first useful view.
const AxisProvider = "provider"

// AxisModule groups by the IaC module a resource was declared in. Large
// estates are mostly modules, and "what does this module own" is a question
// the network topology cannot answer.
const AxisModule = "module"

// AxisAccount groups by the billing and ownership boundary a resource sits in:
// an Azure resource group, a GCP project, an AWS account. These cut across
// network topology rather than nesting inside it, which is why they need an
// axis of their own rather than a place in the network tree.
const AxisAccount = "account"

// EdgeKind distinguishes the three questions oekaki exists to answer.
type EdgeKind string

const (
	// EdgeIACRef means the source's configuration references the target:
	// delete the target and the source breaks.
	EdgeIACRef EdgeKind = "iac_ref"
	// EdgeReachable means the network permits traffic along this path,
	// whether or not anything uses it.
	EdgeReachable EdgeKind = "reachable"
	// EdgeObserved means traffic was actually measured along this path.
	EdgeObserved EdgeKind = "observed"
)

// Valid reports whether k is a kind the schema allows.
func (k EdgeKind) Valid() bool {
	switch k {
	case EdgeIACRef, EdgeReachable, EdgeObserved:
		return true
	}
	return false
}

// Origin says who made a claim.
//
// A parser reading a file is one origin among three. A human reading an
// operations console and a model reading a screenshot are the others, and a
// diagram that cannot tell them apart is worse than one that only has the
// first: it presents somebody's guess with the authority of the code.
type Origin string

const (
	// OriginParser is a claim derived mechanically from an input file.
	OriginParser Origin = "parser"
	// OriginHuman is a claim somebody entered by hand.
	OriginHuman Origin = "human"
	// OriginAI is a claim a model produced. It is kept distinct from a human
	// claim because the failure modes differ: a model is wrong more often,
	// more fluently, and without noticing.
	OriginAI Origin = "ai"
)

// Valid reports whether o is an origin the schema allows.
func (o Origin) Valid() bool {
	switch o {
	case OriginParser, OriginHuman, OriginAI:
		return true
	}
	return false
}

// Rank orders origins for choosing which competing value to display: human
// beats ai beats parser. It is a total order so that the choice does not
// depend on the order overlays happened to be applied in.
//
// Ranking picks what is *shown*. It never discards the alternatives — those
// are recorded in Conflicts, because a diagram that silently picks a winner is
// a diagram that lies quietly.
func (o Origin) Rank() int {
	switch o {
	case OriginHuman:
		return 3
	case OriginAI:
		return 2
	case OriginParser:
		return 1
	}
	return 0
}

// Claim is the provenance of one assertion.
//
// Absent means OriginParser. The overwhelmingly common case therefore costs no
// bytes, and a graph with no overlays applied is almost byte-identical to one
// produced before claims existed.
type Claim struct {
	Origin Origin `json:"origin"`
	Author string `json:"author,omitempty"`

	// Confidence is 0..1. A pointer because unstated and zero are different:
	// a claimant who did not say is not a claimant who said they were sure it
	// was wrong.
	Confidence *float64 `json:"confidence,omitempty"`

	Note string `json:"note,omitempty"`
}

// CoverageState is what is known about a subject's log collection.
//
// Five states, not four. "unknown" is what honesty requires when nobody has
// looked: painting an unassessed resource as a blind spot is the same lie as
// painting a blind spot as covered, and absence of evidence must never render
// as a finding.
type CoverageState string

const (
	// CoverageUnknown means nobody asserted anything about this subject, or
	// what was asserted could not be told apart from other subjects.
	CoverageUnknown CoverageState = "unknown"
	// CoverageFlowing means logs are declared somewhere and something was
	// seen arriving there.
	CoverageFlowing CoverageState = "flowing"
	// CoverageSilent means logs are declared and nothing was seen. Either the
	// thing is idle or the pipeline is broken, and the difference is worth
	// chasing.
	CoverageSilent CoverageState = "silent"
	// CoverageBlind means somebody looked and found no destination at all.
	CoverageBlind CoverageState = "blind"
	// CoverageUndeclared means logs are arriving from something no
	// configuration claims to be shipping them.
	CoverageUndeclared CoverageState = "undeclared"
)

// Valid reports whether s is a state the schema allows.
func (s CoverageState) Valid() bool {
	switch s {
	case CoverageUnknown, CoverageFlowing, CoverageSilent, CoverageBlind, CoverageUndeclared:
		return true
	}
	return false
}

// Coverage records what is known about a node's log collection, and what that
// knowledge rests on. Enrichers own it; parsers must not write here.
type Coverage struct {
	State    CoverageState `json:"state"`
	Reason   string        `json:"reason,omitempty"`
	Evidence []Evidence    `json:"evidence,omitempty"`
}

// Evidence kinds. A declaration is what configuration promises; an observation
// is what was actually seen; a none is somebody reporting they looked and
// found nothing, which is a finding rather than an absence.
const (
	EvidenceDeclared = "declared"
	EvidenceObserved = "observed"
	EvidenceNone     = "none"
)

// Evidence is one declaration or observation, with enough provenance that a
// reader can decide whether to believe it. Validate refuses a coverage state
// with no evidence behind it: a conclusion with no basis is exactly the kind
// of confident wrongness this project exists to avoid.
type Evidence struct {
	Kind string `json:"kind"`

	// Sink is the id of the node logs go to, when there is one.
	Sink   string `json:"sink,omitempty"`
	Stream string `json:"stream,omitempty"`

	// Records is how many were seen. A pointer because "we queried and saw
	// zero" and "no count was available" are different facts, and only the
	// first is a finding.
	Records *float64 `json:"records,omitempty"`

	// Via is how the claimant knew, in their own words.
	Via string `json:"via,omitempty"`

	// Matched names the resolution rule that tied this evidence to its node,
	// so a surprising join can be traced without rerunning anything.
	Matched string `json:"matched,omitempty"`

	Claim *Claim `json:"claim,omitempty"`
}

// ClaimedValue is one competing answer, and who gave it.
type ClaimedValue struct {
	Value string `json:"value"`
	Claim Claim  `json:"claim"`
}

// ConflictTargetKind separates entity conflicts from edge conflicts. Entity ids
// are intentionally open strings, so the kind cannot safely be inferred from a
// delimiter embedded in Target.
type ConflictTargetKind string

const (
	ConflictTargetEntity ConflictTargetKind = "entity"
	ConflictTargetEdge   ConflictTargetKind = "edge"
)

// Valid reports whether k is a conflict target kind the schema understands.
func (k ConflictTargetKind) Valid() bool {
	return k == ConflictTargetEntity || k == ConflictTargetEdge
}

// Conflict records two claims about the same thing that disagree.
//
// It is recorded rather than resolved away. The tool shows one value and
// admits it was contested; deciding which is true is the reader's job, and
// they cannot do it if the disagreement never reaches them.
type Conflict struct {
	// Target is a node or group id when TargetKind is entity, or the opaque,
	// reversible value returned by EdgeKey when TargetKind is edge.
	TargetKind ConflictTargetKind `json:"target_kind"`
	Target     string             `json:"target"`
	Field      string             `json:"field"`
	Claims     []ClaimedValue     `json:"claims"`
}

// OverlayRef records that an overlay was applied.
//
// Window is the caption its author wrote, echoed verbatim. Nothing here is
// read from a clock, so determinism holds — and a diagram that says "blind
// spot" without saying over what period would be a lie of omission.
type OverlayRef struct {
	Source string `json:"source,omitempty"`
	Origin Origin `json:"origin"`
	Author string `json:"author,omitempty"`
	Window string `json:"window,omitempty"`
}

// Graph is the whole IR document.
type Graph struct {
	Version  string    `json:"version"`
	Metadata *Metadata `json:"metadata,omitempty"`
	Axes     []Axis    `json:"axes"`
	Nodes    []Node    `json:"nodes"`
	Edges    []Edge    `json:"edges"`
	Groups   []Group   `json:"groups"`

	// Paths are ordered walks: this one called that one, and that one called
	// the next. See path.go for why an order is an entity here rather than a
	// query somebody runs.
	Paths        []Path               `json:"paths,omitempty"`
	Observations []Observation        `json:"observations,omitempty"`
	LogRecords   []LogRecordSummary   `json:"log_records,omitempty"`
	LogStatus    *LogCollectionStatus `json:"log_status,omitempty"`

	// Conflicts are the disagreements between claims that this document chose
	// to display one side of.
	Conflicts []Conflict `json:"conflicts,omitempty"`
}

// LogCollectionStatus is the safe, graph-level summary of the polling
// component. It contains counts and timestamps, never raw records, query
// bodies, credentials, or backend error payloads.
type LogCollectionStatus struct {
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	Fetched     int    `json:"fetched"`
	Classified  int    `json:"classified"`
	LastError   string `json:"last_error,omitempty"`
}

// Observation is a time-scoped measurement or assessment attached to an
// entity. It is intentionally source-neutral: metrics, security findings,
// network exposure checks, and sensor health all use the same evidence shape.
type Observation struct {
	Subject    string            `json:"subject"`
	Metric     string            `json:"metric"`
	Labels     map[string]string `json:"labels,omitempty"`
	Value      *float64          `json:"value,omitempty"`
	Unit       string            `json:"unit,omitempty"`
	ObservedAt string            `json:"observed_at,omitempty"`
	Window     string            `json:"window,omitempty"`
	State      string            `json:"state,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Threshold  *Threshold        `json:"threshold,omitempty"`
	Evidence   *Claim            `json:"claim,omitempty"`
}

type Threshold struct {
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
}

// LogRecordSummary is classified log metadata. Raw log bodies are explicitly
// absent so a graph can be shared without accidentally carrying customer data.
type LogRecordSummary struct {
	ID              string            `json:"id"`
	Source          string            `json:"source,omitempty"`
	ObservedAt      string            `json:"observed_at,omitempty"`
	Characteristics map[string]string `json:"characteristics,omitempty"`
	Labels          []string          `json:"labels,omitempty"`
}

// Metadata records where a document came from. It carries no timestamp on
// purpose: identical input must produce byte-identical output so that
// generated graphs can be committed and reviewed as diffs.
type Metadata struct {
	Generator     string `json:"generator,omitempty"`
	Source        string `json:"source,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`

	// Scope names the estate this document describes, when the ids in it have
	// been qualified to be unique beyond a single state file. A large
	// organisation splits its infrastructure across many states, and
	// `aws_vpc.main` in one is a different VPC from `aws_vpc.main` in another;
	// without a scope there is no way to tell them apart when combining
	// documents.
	Scope string `json:"scope,omitempty"`

	// Inputs records the repositories or documents selected for a combined
	// graph. Paths are operator-supplied context for local tooling; they are
	// never read as credentials or sent anywhere by the core package.
	Inputs []InputRef `json:"inputs,omitempty"`

	// Overlays records which overlays were applied to produce this document,
	// so a reader can tell a graph that is purely derived from one that
	// carries somebody's assertions.
	Overlays []OverlayRef `json:"overlays,omitempty"`
}

// InputRef tells a consumer, including an AI adapter, which local inputs were
// actually available when this graph was generated.
type InputRef struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Kind          string `json:"kind,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
}

// Axis is one way of grouping the same estate. Infrastructure has no single
// correct hierarchy: the same database is inside a subnet, inside an account,
// inside a module, and owned by a team. Renderers nest by one axis at a time;
// the others remain available for colouring and filtering.
type Axis struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// Node is a resource drawn as a box. Containers are not nodes; see Group.
type Node struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`

	// Description is a short human-readable explanation shown by interactive
	// renderers. It is optional because parsers often cannot infer intent;
	// model or human enrichers may provide it without changing identity.
	Description string `json:"description,omitempty"`

	// Provider is the short provider name, e.g. "aws" or "vsphere". It is what
	// lets containment refuse to cross a boundary, and what the provider axis
	// is built from.
	Provider string `json:"provider,omitempty"`

	// Groups maps an axis id to this node's group path on that axis. A node
	// absent from an axis sits at that axis's top level.
	Groups map[string]string `json:"groups,omitempty"`

	Attrs   map[string]any     `json:"attrs,omitempty"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
	Source  *Source            `json:"source,omitempty"`

	// Coverage is what is known about this node's log collection. Absent
	// means nobody has said anything, which renderers must treat as unknown
	// rather than as a clean bill of health.
	Coverage *Coverage `json:"coverage,omitempty"`

	// Claim is who says this node exists. Absent means a parser found it.
	Claim *Claim `json:"claim,omitempty"`
}

// Edge is a relationship drawn as an arrow. Either end may be a node or a
// group: a reference to a container is a real dependency, and when containment
// cannot express it — across a provider boundary, say — an edge is the only
// honest way left to say it happened.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
	// Relation is an open vocabulary for domain-specific relationships. Kind
	// remains the compatibility field for the original infrastructure graph;
	// new parsers should prefer Relation (calls, reads, writes, exposes, ...)
	// and may use Kind=observed when the relationship is runtime evidence.
	Relation string         `json:"relation,omitempty"`
	Attrs    map[string]any `json:"attrs,omitempty"`

	// Claim is who says this edge exists. Absent means a parser found it.
	Claim *Claim `json:"claim,omitempty"`

	// Suppressed marks an edge somebody asserted is not real.
	//
	// It is a flag rather than a deletion because "a human said this is
	// wrong" and "this never existed" are different facts, and only the first
	// one is true. Renderers draw it faintly; nothing throws it away.
	Suppressed bool `json:"suppressed,omitempty"`
}

// EdgeKey names an edge for a Conflict target. Each component is independently
// base64url encoded and the empty relation is retained as the fourth component.
// The encoding is therefore reversible and collision-free even when an endpoint
// or relation contains the separators used by older IR versions.
func EdgeKey(from, to string, kind EdgeKind, relation ...string) string {
	r := ""
	if len(relation) > 0 {
		r = relation[0]
	}
	encode := base64.RawURLEncoding.EncodeToString
	return "edge:" + strings.Join([]string{
		encode([]byte(from)),
		encode([]byte(to)),
		encode([]byte(kind)),
		encode([]byte(r)),
	}, ".")
}

// ParseEdgeKey reverses EdgeKey. False means key is malformed rather than that
// its edge is absent from any particular graph.
func ParseEdgeKey(key string) (from, to string, kind EdgeKind, relation string, ok bool) {
	if !strings.HasPrefix(key, "edge:") {
		return "", "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(key, "edge:"), ".")
	if len(parts) != 4 {
		return "", "", "", "", false
	}
	decoded := make([]string, len(parts))
	for i, part := range parts {
		b, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil || !utf8.Valid(b) {
			return "", "", "", "", false
		}
		decoded[i] = string(b)
	}
	decodedKind := EdgeKind(decoded[2])
	if decoded[0] == "" || decoded[1] == "" || !decodedKind.Valid() || EdgeKey(decoded[0], decoded[1], decodedKind, decoded[3]) != key {
		return "", "", "", "", false
	}
	return decoded[0], decoded[1], decodedKind, decoded[3], true
}

// HasConflictTarget reports whether target names an entity or edge in the
// graph. Kind is mandatory because an entity id may equal an encoded edge key;
// guessing would silently attach a conflict to the wrong graph object.
func (g *Graph) HasConflictTarget(target string, kind ConflictTargetKind) bool {
	switch kind {
	case ConflictTargetEntity:
		if _, ok := g.Node(target); ok {
			return true
		}
		_, ok := g.Group(target)
		return ok
	case ConflictTargetEdge:
		from, to, edgeKind, relation, ok := ParseEdgeKey(target)
		if !ok {
			return false
		}
		for _, e := range g.Edges {
			if e.From == from && e.To == to && e.Kind == edgeKind && e.Relation == relation {
				return true
			}
		}
	}
	return false
}

func legacyEdgeKey(e Edge) string {
	key := e.From + "|" + e.To + "|" + string(e.Kind)
	if e.Relation != "" {
		key += "|" + e.Relation
	}
	return key
}

func (g *Graph) resolveLegacyConflictTarget(target string) (ConflictTargetKind, string, int) {
	type candidate struct {
		kind   ConflictTargetKind
		target string
	}
	candidates := map[candidate]struct{}{}
	if _, ok := g.Node(target); ok {
		candidates[candidate{kind: ConflictTargetEntity, target: target}] = struct{}{}
	}
	if _, ok := g.Group(target); ok {
		candidates[candidate{kind: ConflictTargetEntity, target: target}] = struct{}{}
	}
	for _, e := range g.Edges {
		legacy := legacyEdgeKey(e)
		if target == legacy || (e.Relation == "" && target == legacy+"|") {
			candidates[candidate{kind: ConflictTargetEdge, target: EdgeKey(e.From, e.To, e.Kind, e.Relation)}] = struct{}{}
		}
	}
	if len(candidates) != 1 {
		return "", "", len(candidates)
	}
	for c := range candidates {
		return c.kind, c.target, 1
	}
	panic("unreachable")
}

func (g *Graph) migrateLegacyConflictTargets() error {
	for i := range g.Conflicts {
		conflict := &g.Conflicts[i]
		if conflict.TargetKind != "" {
			continue
		}
		kind, target, candidates := g.resolveLegacyConflictTarget(conflict.Target)
		switch candidates {
		case 0:
			return fmt.Errorf("conflict on %q: legacy target names nothing in this graph", conflict.Target)
		case 1:
			conflict.TargetKind = kind
			conflict.Target = target
		default:
			return fmt.Errorf("conflict on %q: ambiguous legacy target names %d entities or edges; regenerate the graph with target_kind", conflict.Target, candidates)
		}
	}
	return nil
}

// Group is a container on one axis: a VPC, a Kubernetes namespace, a vSphere
// datacenter, an IaC module. The Groups slice is the authoritative record of
// nesting; the paths in Node.Groups are derived from it.
type Group struct {
	ID     string         `json:"id"`
	Axis   string         `json:"axis"`
	Type   string         `json:"type"`
	Label  string         `json:"label"`
	Parent *string        `json:"parent"`
	Attrs  map[string]any `json:"attrs,omitempty"`
	Source *Source        `json:"source,omitempty"`

	// Claim is who says this container exists. Absent means a parser found it.
	Claim *Claim `json:"claim,omitempty"`
}

// Source points back at the configuration a node or group came from, so a
// diagram can link to the line of code that produced it.
type Source struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

// New returns an empty graph with the current version and non-nil slices, so
// that it marshals to `[]` rather than `null`.
func New() *Graph {
	return &Graph{
		Version: Version,
		Axes:    []Axis{},
		Nodes:   []Node{},
		Edges:   []Edge{},
		Groups:  []Group{},
	}
}

// Node returns the node with the given id.
func (g *Graph) Node(id string) (*Node, bool) {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i], true
		}
	}
	return nil, false
}

// Group returns the group with the given id. Ids are unique across nodes and
// groups alike, so an id identifies one thing regardless of which it is.
func (g *Graph) Group(id string) (*Group, bool) {
	for i := range g.Groups {
		if g.Groups[i].ID == id {
			return &g.Groups[i], true
		}
	}
	return nil, false
}

// HasAxis reports whether the document declares the given axis.
func (g *Graph) HasAxis(id string) bool {
	for _, a := range g.Axes {
		if a.ID == id {
			return true
		}
	}
	return false
}

// AxisOrDefault returns the axis to nest by: the one asked for if the document
// has it, otherwise the network axis, otherwise whichever axis exists. It
// returns "" when the document has no axes at all, which renderers treat as a
// flat drawing rather than an error.
func (g *Graph) AxisOrDefault(want string) string {
	if want != "" && g.HasAxis(want) {
		return want
	}
	if want == "" && g.HasAxis(AxisNetwork) {
		return AxisNetwork
	}
	if want != "" {
		return ""
	}
	if len(g.Axes) > 0 {
		return g.Axes[0].ID
	}
	return ""
}

// GroupPath builds the slash-separated ancestry path for a group id, from the
// outermost ancestor down to id itself. It returns an error if the ancestry is
// broken or cyclic.
func (g *Graph) GroupPath(id string) (string, error) {
	var parts []string
	seen := map[string]bool{}
	for cur := id; cur != ""; {
		if seen[cur] {
			return "", fmt.Errorf("group %q: cycle in parent chain", id)
		}
		seen[cur] = true

		grp, ok := g.Group(cur)
		if !ok {
			return "", fmt.Errorf("group %q: unknown ancestor %q", id, cur)
		}
		parts = append(parts, grp.ID)
		if grp.Parent == nil {
			break
		}
		cur = *grp.Parent
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, GroupSeparator), nil
}

// Children returns the ids of groups on an axis whose parent is id. Pass "" for
// that axis's top-level groups. The result is sorted.
func (g *Graph) Children(axis, id string) []string {
	var out []string
	for _, grp := range g.Groups {
		if grp.Axis != axis {
			continue
		}
		switch {
		case id == "" && grp.Parent == nil:
			out = append(out, grp.ID)
		case id != "" && grp.Parent != nil && *grp.Parent == id:
			out = append(out, grp.ID)
		}
	}
	sort.Strings(out)
	return out
}

// NodesIn returns the nodes whose path on an axis is exactly path, sorted by
// id. Pass "" for nodes that sit outside every container on that axis.
func (g *Graph) NodesIn(axis, path string) []*Node {
	var out []*Node
	for i := range g.Nodes {
		if g.Nodes[i].Groups[axis] == path {
			out = append(out, &g.Nodes[i])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// EdgesOfKind returns the edges of a single kind, in stored order.
func (g *Graph) EdgesOfKind(k EdgeKind) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

// Normalize sorts every collection into a canonical order and drops duplicate
// edges. Determinism is a design requirement, not a nicety: users are meant to
// commit generated graphs and review them as diffs, which only works if the
// same input always produces the same bytes.
func (g *Graph) Normalize() {
	if g.Axes == nil {
		g.Axes = []Axis{}
	}
	if g.Nodes == nil {
		g.Nodes = []Node{}
	}
	if g.Edges == nil {
		g.Edges = []Edge{}
	}
	if g.Groups == nil {
		g.Groups = []Group{}
	}
	if g.LogRecords == nil {
		g.LogRecords = []LogRecordSummary{}
	}

	sort.SliceStable(g.Axes, func(i, j int) bool { return g.Axes[i].ID < g.Axes[j].ID })
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.SliceStable(g.Groups, func(i, j int) bool {
		if g.Groups[i].Axis != g.Groups[j].Axis {
			return g.Groups[i].Axis < g.Groups[j].Axis
		}
		return g.Groups[i].ID < g.Groups[j].ID
	})
	sort.SliceStable(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Relation != b.Relation {
			return a.Relation < b.Relation
		}
		return edgeAssertionLess(a, b)
	})
	g.normalizePaths()
	sort.SliceStable(g.Observations, func(i, j int) bool {
		a, b := g.Observations[i], g.Observations[j]
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.ObservedAt != b.ObservedAt {
			return a.ObservedAt < b.ObservedAt
		}
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		if a.State != b.State {
			return a.State < b.State
		}
		return observationSortKey(a) < observationSortKey(b)
	})
	sort.SliceStable(g.LogRecords, func(i, j int) bool {
		if g.LogRecords[i].ObservedAt != g.LogRecords[j].ObservedAt {
			return g.LogRecords[i].ObservedAt < g.LogRecords[j].ObservedAt
		}
		return g.LogRecords[i].ID < g.LogRecords[j].ID
	})

	// Duplicates are merged rather than dropped. Two sources finding the same
	// edge is the normal case once overlays exist, and the second one may be
	// carrying something the first did not: a suppression, or a claim from a
	// different origin. Discarding it would lose that silently.
	deduped := g.Edges[:0]
	var prev *Edge
	var assertions []Edge
	recordAssertions := func() {
		if len(assertions) < 2 {
			return
		}
		var positive, suppressed bool
		claims := make([]ClaimedValue, 0, len(assertions))
		for _, assertion := range assertions {
			if assertion.Suppressed {
				suppressed = true
			} else {
				positive = true
			}
			claims = append(claims, ClaimedValue{
				Value: boolValue(assertion.Suppressed), Claim: claimOrParser(assertion.Claim),
			})
		}
		if positive && suppressed {
			first := assertions[0]
			g.Conflicts = append(g.Conflicts, Conflict{
				TargetKind: ConflictTargetEdge,
				Target:     EdgeKey(first.From, first.To, first.Kind, first.Relation),
				Field:      "suppressed",
				Claims:     claims,
			})
		}
	}
	for i := range g.Edges {
		e := g.Edges[i]
		if prev != nil && prev.Kind == e.Kind && prev.From == e.From && prev.To == e.To && prev.Relation == e.Relation {
			assertions = append(assertions, e)
			g.mergeEdge(prev, e)
			continue
		}
		recordAssertions()
		deduped = append(deduped, e)
		prev = &deduped[len(deduped)-1]
		assertions = []Edge{e}
	}
	recordAssertions()
	g.Edges = deduped

	for i := range g.Nodes {
		if len(g.Nodes[i].Groups) == 0 {
			g.Nodes[i].Groups = nil
		}
		if c := g.Nodes[i].Coverage; c != nil {
			sortEvidence(c.Evidence)
		}
	}

	// The record of which overlays were applied is sorted too. Application
	// order is a command line rather than a fact about the estate, and leaving
	// it in the output would mean the same overlays given in a different order
	// produced different bytes — which is the guarantee this function exists
	// to keep.
	if g.Metadata != nil {
		sort.SliceStable(g.Metadata.Overlays, func(i, j int) bool {
			a, b := g.Metadata.Overlays[i], g.Metadata.Overlays[j]
			if a.Source != b.Source {
				return a.Source < b.Source
			}
			if a.Origin != b.Origin {
				return a.Origin < b.Origin
			}
			if a.Author != b.Author {
				return a.Author < b.Author
			}
			return a.Window < b.Window
		})
	}

	sort.SliceStable(g.Conflicts, func(i, j int) bool {
		if g.Conflicts[i].TargetKind != g.Conflicts[j].TargetKind {
			return g.Conflicts[i].TargetKind < g.Conflicts[j].TargetKind
		}
		if g.Conflicts[i].Target != g.Conflicts[j].Target {
			return g.Conflicts[i].Target < g.Conflicts[j].Target
		}
		return g.Conflicts[i].Field < g.Conflicts[j].Field
	})
	// A conflict is identified by the target and field, just like an edge is
	// identified by its endpoints, kind, and relation. Merging more than two
	// assertions can discover the same disagreement repeatedly; keep one record
	// containing every distinct claimed value instead of emitting pairwise
	// fragments whose count depends on merge order.
	dedupedConflicts := g.Conflicts[:0]
	for _, conflict := range g.Conflicts {
		if len(dedupedConflicts) == 0 ||
			dedupedConflicts[len(dedupedConflicts)-1].TargetKind != conflict.TargetKind ||
			dedupedConflicts[len(dedupedConflicts)-1].Target != conflict.Target ||
			dedupedConflicts[len(dedupedConflicts)-1].Field != conflict.Field {
			conflict.Claims = uniqueClaimedValues(conflict.Claims)
			dedupedConflicts = append(dedupedConflicts, conflict)
			continue
		}
		current := &dedupedConflicts[len(dedupedConflicts)-1]
		current.Claims = uniqueClaimedValues(append(current.Claims, conflict.Claims...))
	}
	// A schema-valid input may repeat the exact same claimed value. Once those
	// duplicates collapse, fewer than two claims is agreement rather than a
	// conflict; retaining it would make Normalize produce a graph Validate
	// rejects.
	keptConflicts := dedupedConflicts[:0]
	for _, conflict := range dedupedConflicts {
		if len(conflict.Claims) < 2 {
			continue
		}
		keptConflicts = append(keptConflicts, conflict)
	}
	g.Conflicts = keptConflicts
	for i := range g.Conflicts {
		preferredValue := ""
		if g.Conflicts[i].TargetKind == ConflictTargetEdge && g.Conflicts[i].Field == "suppressed" {
			preferredValue = "true"
		}
		sortClaimedValues(g.Conflicts[i].Claims, preferredValue)
	}
	if len(g.Conflicts) == 0 {
		g.Conflicts = nil
	}
}

// mergeEdge folds b into a. Suppression is the one field where the two can
// genuinely disagree — one source says the edge is real, another says it is
// not — so that disagreement is recorded rather than resolved into silence.
// Two sources merely both finding the edge is agreement, not conflict.
func (g *Graph) mergeEdge(a *Edge, b Edge) {
	// Suppression is fail-safe: once any source marks an edge as not real, a
	// duplicate positive assertion cannot silently re-enable it. Keep the best
	// claim among assertions for the effective value so the edge still carries
	// useful provenance.
	if b.Suppressed && !a.Suppressed {
		a.Suppressed = true
		a.Claim = b.Claim
	} else if a.Suppressed == b.Suppressed && edgeClaimLess(b, *a) {
		a.Claim = b.Claim
	}
	a.Attrs = mergeAttrs(a.Attrs, b.Attrs)
}

// edgeAssertionLess provides a total order within one edge identity. Duplicate
// edges are sorted by it before merging so arrival order cannot affect either
// the selected assertion or the conflicts recorded along the way.
func edgeAssertionLess(a, b Edge) bool {
	if edgeClaimLess(a, b) {
		return true
	}
	if edgeClaimLess(b, a) {
		return false
	}
	return attrsKey(a.Attrs) < attrsKey(b.Attrs)
}

// edgeClaimLess orders competing whole-edge assertions. Origin rank decides
// which claim is preferred within the same asserted value; equal-ranked claims
// use their complete canonical provenance and then suppression as a
// deterministic, fail-safe tie-breaker.
func edgeClaimLess(a, b Edge) bool {
	ac, bc := claimOrParser(a.Claim), claimOrParser(b.Claim)
	if ac.Origin.Rank() != bc.Origin.Rank() {
		return ac.Origin.Rank() > bc.Origin.Rank()
	}
	if claimLess(ac, bc) {
		return true
	}
	if claimLess(bc, ac) {
		return false
	}
	if a.Suppressed != b.Suppressed {
		return a.Suppressed
	}
	if (a.Claim == nil) != (b.Claim == nil) {
		return a.Claim == nil
	}
	return false
}

// mergeAttrs preserves non-conflicting detail from every duplicate edge. When
// two sources assign different JSON values to the same parser-owned key, the
// lexicographically smaller canonical JSON value wins, making the operation
// commutative and associative without inventing provenance that Attrs cannot
// represent.
func mergeAttrs(a, b map[string]any) map[string]any {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]any, len(a)+len(b))
	for key, value := range a {
		out[key] = value
	}
	for key, value := range b {
		current, exists := out[key]
		if !exists || jsonValueKey(value) < jsonValueKey(current) {
			out[key] = value
		}
	}
	return out
}

func attrsKey(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	b, _ := json.Marshal(attrs)
	return string(b)
}

func jsonValueKey(value any) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func boolValue(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func observationSortKey(o Observation) string {
	b, _ := json.Marshal(o)
	return string(b)
}

// claimOrParser reads an absent claim as the parser's, which is what absence
// means throughout the IR.
func claimOrParser(c *Claim) Claim {
	if c == nil {
		return Claim{Origin: OriginParser}
	}
	return *c
}

// sortEvidence orders one node's evidence.
//
// The key covers every field that is written out, not just the ones that
// identify a sink. Two pieces of evidence can agree on kind, sink and stream
// and still be different claims — "looked on the console" and "queried the
// index" are not interchangeable, and neither are the same observation made by
// two different people. Any pair the key cannot separate is left in the order
// it arrived in, which is the order the overlays were named on a command line.
// That is not a fact about the estate, so it must not reach the output.
func sortEvidence(ev []Evidence) {
	sort.SliceStable(ev, func(i, j int) bool {
		a, b := ev[i], ev[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Sink != b.Sink {
			return a.Sink < b.Sink
		}
		if a.Stream != b.Stream {
			return a.Stream < b.Stream
		}
		if a.Via != b.Via {
			return a.Via < b.Via
		}
		if optionalKey(a.Records) != optionalKey(b.Records) {
			return optionalKey(a.Records) < optionalKey(b.Records)
		}
		if a.Matched != b.Matched {
			return a.Matched < b.Matched
		}
		return claimLess(claimOrParser(a.Claim), claimOrParser(b.Claim))
	})
}

// claimLess orders two claims by every field they carry, so that evidence
// differing only in who claimed it still has one settled order.
func claimLess(a, b Claim) bool {
	if a.Origin != b.Origin {
		return a.Origin < b.Origin
	}
	if a.Author != b.Author {
		return a.Author < b.Author
	}
	if optionalKey(a.Confidence) != optionalKey(b.Confidence) {
		return optionalKey(a.Confidence) < optionalKey(b.Confidence)
	}
	return a.Note < b.Note
}

// optionalKey orders an optional number, putting "not stated" before any
// value including zero — the two are different facts, and an ordering that
// cannot tell them apart would leave them tied.
func optionalKey(r *float64) float64 {
	if r == nil {
		return math.Inf(-1)
	}
	return *r
}

// sortClaimedValues puts the effective value first when one is supplied, then
// orders by origin, every provenance field, and value. Suppression conflicts
// use this to reflect their fail-safe effective value without making arrival
// order part of the document.
func sortClaimedValues(cv []ClaimedValue, preferredValue string) {
	sort.SliceStable(cv, func(i, j int) bool {
		a, b := cv[i], cv[j]
		if preferredValue != "" && (a.Value == preferredValue) != (b.Value == preferredValue) {
			return a.Value == preferredValue
		}
		if a.Claim.Origin.Rank() != b.Claim.Origin.Rank() {
			return a.Claim.Origin.Rank() > b.Claim.Origin.Rank()
		}
		if claimLess(a.Claim, b.Claim) {
			return true
		}
		if claimLess(b.Claim, a.Claim) {
			return false
		}
		return a.Value < b.Value
	})
}

func uniqueClaimedValues(values []ClaimedValue) []ClaimedValue {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]bool, len(values))
	result := values[:0]
	for _, value := range values {
		key := claimedValueKey(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func claimedValueKey(value ClaimedValue) string {
	confidence := "unset"
	if value.Claim.Confidence != nil {
		confidence = fmt.Sprintf("%016x", math.Float64bits(*value.Claim.Confidence))
	}
	return fmt.Sprintf("%q|%q|%q|%s|%q", value.Value, value.Claim.Origin, value.Claim.Author, confidence, value.Claim.Note)
}

// Validate checks the invariants that JSON Schema cannot express: ids that are
// unique across nodes and groups alike, edges that land on something real, and
// a group hierarchy that is actually a forest on each axis. It reports every
// problem it finds rather than stopping at the first.
func (g *Graph) Validate() error {
	var problems []string

	if g.Version != Version {
		problems = append(problems, fmt.Sprintf(
			"version: got %q, want %q — this document was produced by a different oekaki; regenerate it from its source",
			g.Version, Version))
	}

	axes := map[string]bool{}
	for _, a := range g.Axes {
		if a.ID == "" {
			problems = append(problems, "axis: empty id")
			continue
		}
		if axes[a.ID] {
			problems = append(problems, fmt.Sprintf("axis %q: duplicate id", a.ID))
		}
		axes[a.ID] = true
	}

	// One namespace for nodes and groups. Edges may point at either, so an id
	// has to identify exactly one thing for that to be unambiguous.
	ids := map[string]bool{}
	nodeIDs := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == "" {
			problems = append(problems, "node: empty id")
			continue
		}
		if ids[n.ID] {
			problems = append(problems, fmt.Sprintf("node %q: duplicate id", n.ID))
		}
		ids[n.ID], nodeIDs[n.ID] = true, true
		if n.Type == "" {
			problems = append(problems, fmt.Sprintf("node %q: empty type", n.ID))
		}
	}

	groupIDs := map[string]bool{}
	for _, grp := range g.Groups {
		if grp.ID == "" {
			problems = append(problems, "group: empty id")
			continue
		}
		if strings.Contains(grp.ID, GroupSeparator) {
			problems = append(problems, fmt.Sprintf("group %q: id must not contain %q", grp.ID, GroupSeparator))
		}
		if ids[grp.ID] {
			problems = append(problems, fmt.Sprintf("group %q: duplicate id", grp.ID))
		}
		ids[grp.ID], groupIDs[grp.ID] = true, true

		if grp.Axis == "" {
			problems = append(problems, fmt.Sprintf("group %q: no axis", grp.ID))
		} else if !axes[grp.Axis] {
			problems = append(problems, fmt.Sprintf("group %q: undeclared axis %q", grp.ID, grp.Axis))
		}
	}

	for _, grp := range g.Groups {
		if grp.Parent == nil {
			continue
		}
		if *grp.Parent == grp.ID {
			problems = append(problems, fmt.Sprintf("group %q: is its own parent", grp.ID))
			continue
		}
		parent, ok := g.Group(*grp.Parent)
		if !ok {
			problems = append(problems, fmt.Sprintf("group %q: unknown parent %q", grp.ID, *grp.Parent))
			continue
		}
		// A hierarchy that changes axis halfway up is not a hierarchy.
		if parent.Axis != grp.Axis {
			problems = append(problems, fmt.Sprintf("group %q: parent %q is on axis %q, not %q", grp.ID, parent.ID, parent.Axis, grp.Axis))
		}
	}

	// Only worth walking ancestry once the parent pointers are known good;
	// otherwise every group reports the same missing-parent error twice.
	if len(problems) == 0 {
		for _, grp := range g.Groups {
			if _, err := g.GroupPath(grp.ID); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}

	for _, e := range g.Edges {
		if !e.Kind.Valid() {
			problems = append(problems, fmt.Sprintf("edge %s -> %s: unknown kind %q", e.From, e.To, e.Kind))
		}
		if !ids[e.From] {
			problems = append(problems, fmt.Sprintf("edge %s -> %s: unknown source %q", e.From, e.To, e.From))
		}
		if !ids[e.To] {
			problems = append(problems, fmt.Sprintf("edge %s -> %s: unknown target %q", e.From, e.To, e.To))
		}
	}

	if len(problems) == 0 {
		valid := map[string]map[string]bool{}
		for _, grp := range g.Groups {
			p, err := g.GroupPath(grp.ID)
			if err != nil {
				continue
			}
			if valid[grp.Axis] == nil {
				valid[grp.Axis] = map[string]bool{"": true}
			}
			valid[grp.Axis][p] = true
		}
		for _, n := range g.Nodes {
			for axis, path := range n.Groups {
				if !axes[axis] {
					problems = append(problems, fmt.Sprintf("node %q: undeclared axis %q", n.ID, axis))
					continue
				}
				if path == "" {
					continue
				}
				if !valid[axis][path] {
					problems = append(problems, fmt.Sprintf("node %q: group path %q does not exist on axis %q", n.ID, path, axis))
				}
			}
		}
	}

	for _, n := range g.Nodes {
		problems = append(problems, checkClaim(n.Claim, fmt.Sprintf("node %q", n.ID))...)
		problems = append(problems, g.checkCoverage(&n, ids)...)
	}
	problems = append(problems, g.checkPaths(nodeIDs)...)

	// A measurement may be about a route rather than about one box, and a
	// route is named by its key. The key is only a subject when the document
	// carries the path it names: an observation about a walk nobody wrote
	// down is a reading with nothing to attach it to.
	pathKeys := make(map[string]bool, len(g.Paths))
	for _, p := range g.Paths {
		pathKeys[p.Key()] = true
	}
	for i, o := range g.Observations {
		where := fmt.Sprintf("observation %d", i)
		if o.Subject == "" {
			problems = append(problems, where+": empty subject")
		} else if !ids[o.Subject] && !pathKeys[o.Subject] {
			problems = append(problems, fmt.Sprintf("%s: unknown subject %q", where, o.Subject))
		}
		if o.Metric == "" {
			problems = append(problems, where+": empty metric")
		}
		if o.Value != nil && (math.IsNaN(*o.Value) || math.IsInf(*o.Value, 0)) {
			problems = append(problems, where+": value must be finite")
		}
		if o.Threshold != nil {
			if !validThresholdOperator(o.Threshold.Operator) {
				problems = append(problems, fmt.Sprintf("%s: unknown threshold operator %q", where, o.Threshold.Operator))
			}
			if math.IsNaN(o.Threshold.Value) || math.IsInf(o.Threshold.Value, 0) {
				problems = append(problems, where+": threshold value must be finite")
			}
		}
		problems = append(problems, checkClaim(o.Evidence, where)...)
	}
	for _, e := range g.Edges {
		problems = append(problems, checkClaim(e.Claim, fmt.Sprintf("edge %s -> %s", e.From, e.To))...)
	}
	for _, grp := range g.Groups {
		problems = append(problems, checkClaim(grp.Claim, fmt.Sprintf("group %q", grp.ID))...)
	}

	for _, c := range g.Conflicts {
		where := fmt.Sprintf("conflict on %q", c.Target)
		if !c.TargetKind.Valid() {
			problems = append(problems, fmt.Sprintf("%s: unknown target kind %q", where, c.TargetKind))
		} else if c.TargetKind == ConflictTargetEdge {
			_, _, _, _, ok := ParseEdgeKey(c.Target)
			if !ok {
				problems = append(problems, where+": malformed edge target")
			} else if !g.HasConflictTarget(c.Target, c.TargetKind) {
				problems = append(problems, where+": target names nothing in this graph")
			}
		} else if !g.HasConflictTarget(c.Target, c.TargetKind) {
			problems = append(problems, where+": target names nothing in this graph")
		}
		if c.Field == "" {
			problems = append(problems, where+": no field")
		}
		if len(c.Claims) < 2 {
			problems = append(problems, where+": a conflict needs at least two competing claims")
		}
		for _, cv := range c.Claims {
			problems = append(problems, checkClaim(&cv.Claim, where)...)
		}
	}

	if g.Metadata != nil {
		for _, o := range g.Metadata.Overlays {
			if !o.Origin.Valid() {
				problems = append(problems, fmt.Sprintf("overlay %q: unknown origin %q", o.Source, o.Origin))
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("invalid graph:\n  %s", strings.Join(problems, "\n  "))
}

func validThresholdOperator(operator string) bool {
	switch operator {
	case ">", ">=", "<", "<=", "==", "!=":
		return true
	default:
		return false
	}
}

func checkClaim(c *Claim, where string) []string {
	if c == nil {
		return nil
	}
	var problems []string
	if !c.Origin.Valid() {
		problems = append(problems, fmt.Sprintf("%s: unknown claim origin %q", where, c.Origin))
	}
	if c.Confidence != nil {
		if math.IsNaN(*c.Confidence) || math.IsInf(*c.Confidence, 0) {
			problems = append(problems, fmt.Sprintf("%s: confidence must be finite", where))
		} else if *c.Confidence < 0 || *c.Confidence > 1 {
			problems = append(problems, fmt.Sprintf("%s: confidence %v is outside 0..1", where, *c.Confidence))
		}
	}
	return problems
}

// checkCoverage enforces that a state has a basis. A finding with no evidence
// behind it cannot be argued with, and a coverage map whose findings cannot be
// argued with is not worth having.
func (g *Graph) checkCoverage(n *Node, ids map[string]bool) []string {
	c := n.Coverage
	if c == nil {
		return nil
	}

	var problems []string
	where := fmt.Sprintf("node %q coverage", n.ID)

	if !c.State.Valid() {
		problems = append(problems, fmt.Sprintf("%s: unknown state %q", where, c.State))
	}

	var hasNone bool
	for _, e := range c.Evidence {
		switch e.Kind {
		case EvidenceDeclared, EvidenceObserved:
		case EvidenceNone:
			hasNone = true
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown evidence kind %q", where, e.Kind))
		}
		if e.Sink != "" && !ids[e.Sink] {
			problems = append(problems, fmt.Sprintf("%s: evidence names unknown sink %q", where, e.Sink))
		}
		problems = append(problems, checkClaim(e.Claim, where+" evidence")...)
	}

	switch c.State {
	case CoverageFlowing, CoverageSilent, CoverageUndeclared:
		if len(c.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("%s: state %q with no evidence behind it", where, c.State))
		}
	case CoverageBlind:
		if !hasNone {
			problems = append(problems, fmt.Sprintf(
				"%s: blind without a %q evidence — a blind spot has to be something somebody looked for",
				where, EvidenceNone))
		}
	case CoverageUnknown:
		if len(c.Evidence) > 0 {
			problems = append(problems, fmt.Sprintf("%s: unknown, yet carrying evidence", where))
		}
	}
	return problems
}

// ApplyScope qualifies every id with the estate's name. An empty scope leaves
// the graph alone.
//
// A resource address is only unique within one state file. An organisation of
// any size has many, and `aws_vpc.main` in the platform team's state is not the
// same VPC as `aws_vpc.main` in the data team's. Combining those documents
// without qualifying the ids would silently merge unrelated resources, so the
// qualification is offered here rather than left to be discovered later.
//
// It lives on the graph because every parser that offers a scope has to do the
// same thing with it, and a second copy of "what scope means" would eventually
// mean something else.
func (g *Graph) ApplyScope(scope string) {
	if scope == "" {
		return
	}
	qualify := func(id string) string { return scope + ":" + id }

	for i := range g.Nodes {
		g.Nodes[i].ID = qualify(g.Nodes[i].ID)
		for axis, path := range g.Nodes[i].Groups {
			parts := strings.Split(path, GroupSeparator)
			for j := range parts {
				parts[j] = qualify(parts[j])
			}
			g.Nodes[i].Groups[axis] = strings.Join(parts, GroupSeparator)
		}
	}
	for i := range g.Groups {
		g.Groups[i].ID = qualify(g.Groups[i].ID)
		if g.Groups[i].Parent != nil {
			p := qualify(*g.Groups[i].Parent)
			g.Groups[i].Parent = &p
		}
	}
	for i := range g.Edges {
		g.Edges[i].From = qualify(g.Edges[i].From)
		g.Edges[i].To = qualify(g.Edges[i].To)
	}
	g.QualifyPaths(qualify)
	// A reading names what it is about, and after a rename that name is a
	// different string. Leaving it alone leaves the document pointing at ids
	// that no longer exist.
	for i := range g.Observations {
		if renamed, isPath := QualifySubject(g.Observations[i].Subject, qualify); isPath {
			g.Observations[i].Subject = renamed
			continue
		}
		g.Observations[i].Subject = qualify(g.Observations[i].Subject)
	}
}
