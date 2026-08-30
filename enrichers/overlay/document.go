// Package overlay applies assertions a human or a model wrote to a graph.
//
// An overlay is the answer to a limitation the rest of the pipeline cannot fix
// on its own: what is written in the code is not the whole truth, and is not
// automatically more true than what somebody can see on an operations console.
// Parsers recover what a file happens to record. An overlay carries everything
// else — a connection that exists but is written down nowhere, a log stream
// arriving from something nobody modelled, a rule that was removed by hand.
//
// oekaki never writes one. It consumes overlays and never generates them,
// which is what lets a model participate without costing the project its
// determinism: whatever produced the file was free to be non-deterministic,
// and once the file exists identical input still yields identical bytes.
package overlay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/providers"
	"github.com/imohiyoko/oekaki/schema"
)

// Assertion kinds.
const (
	AssertLogsDeclared = "logs.declared"
	AssertLogsObserved = "logs.observed"
	AssertLogsNone     = "logs.none"
	AssertEdge         = "edge"
	AssertEdgeSuppress = "edge.suppress"
	AssertNode         = "node"
)

// Document is one overlay file.
type Document struct {
	Kind       string      `json:"kind"`
	Version    string      `json:"version"`
	Metadata   *Metadata   `json:"metadata,omitempty"`
	Sinks      []Sink      `json:"sinks,omitempty"`
	Assertions []Assertion `json:"assertions"`

	// Source is the file this came from, for reports. Not part of the format.
	Source string `json:"-"`
}

// Metadata supplies defaults for every assertion in the document. Repeating
// an origin on fifty assertions is fifty chances to typo it.
type Metadata struct {
	Origin core.Origin `json:"origin,omitempty"`
	Author string      `json:"author,omitempty"`
	Note   string      `json:"note,omitempty"`
	Window string      `json:"window,omitempty"`
}

// Sink is a log destination this document refers to.
type Sink struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
	Name string `json:"name"`
}

// Selector names something in the graph without knowing its IR id.
type Selector map[string]string

// Assertion is one claim. A single flat struct with a discriminator rather
// than an interface and a type per kind: it makes the JSON Schema expressible
// with if/then, it makes an error report a real JSON path, and it is the
// shape a generator gets right most often. The type safety that costs is
// bought back by Validate, which rejects a field that does not belong to the
// kind being asserted — and names it, which is more use to a generator than a
// Go type error would have been.
type Assertion struct {
	Assert string `json:"assert"`

	Subject Selector `json:"subject,omitempty"`
	From    Selector `json:"from,omitempty"`
	To      Selector `json:"to,omitempty"`

	Sink    string   `json:"sink,omitempty"`
	Stream  string   `json:"stream,omitempty"`
	Records *float64 `json:"records,omitempty"`
	Via     string   `json:"via,omitempty"`

	Kind core.EdgeKind `json:"kind,omitempty"`
	Type string        `json:"type,omitempty"`
	Name string        `json:"name,omitempty"`

	Origin     core.Origin `json:"origin,omitempty"`
	Author     string      `json:"author,omitempty"`
	Confidence *float64    `json:"confidence,omitempty"`
	Note       string      `json:"note,omitempty"`

	// present records which keys the file actually contained, so that a field
	// which is merely meaningless can be told from one that was left out.
	present map[string]bool
}

// UnmarshalJSON decodes an assertion and remembers which keys were given.
func (a *Assertion) UnmarshalJSON(data []byte) error {
	type plain Assertion
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}

	*a = Assertion(p)
	a.present = make(map[string]bool, len(keys))
	for k := range keys {
		a.present[k] = true
	}
	return nil
}

// meaningful lists, per assertion kind, the fields that mean anything.
// Everything not here for a kind is rejected by name.
var meaningful = map[string][]string{
	AssertLogsDeclared: {"subject", "sink", "stream", "via"},
	AssertLogsObserved: {"subject", "sink", "stream", "records", "via"},
	AssertLogsNone:     {"subject", "via"},
	AssertEdge:         {"from", "to", "kind"},
	AssertEdgeSuppress: {"from", "to", "kind"},
	AssertNode:         {"subject", "type", "name"},
}

// alwaysMeaningful are the envelope fields every assertion may carry.
var alwaysMeaningful = []string{"assert", "origin", "author", "confidence", "note"}

// Parse validates a document against the overlay schema, decodes it, and then
// checks what the schema cannot.
//
// Schema first, deliberately: the error a user sees is then the one with a
// JSON path in it, rather than a Go decoder complaining that a number is not
// a string. Whoever wrote the file — person or model — needs to be told where
// to look.
func Parse(raw []byte, source string) (*Document, error) {
	// Selector keys are checked before the schema gets a say, for one reason:
	// the schema rejects an unknown key with "additionalProperties 'svc' not
	// allowed", which names the mistake but not the alternatives. A generator
	// that guessed "svc" cannot correct itself from that. Everything else is
	// left to the schema, whose JSON paths are better than anything written
	// here would be.
	if err := precheckSelectors(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if err := schema.ValidateOverlay(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}

	var doc Document
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	doc.Source = source

	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &doc, nil
}

// precheckSelectors reports an unknown selector key with the vocabulary
// attached, before the schema rejects it less helpfully.
//
// It is deliberately tolerant of everything else: a document too malformed to
// walk is simply handed to the schema, which will say so properly.
func precheckSelectors(raw []byte) error {
	var probe struct {
		Assertions []map[string]json.RawMessage `json:"assertions"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil
	}

	var problems []string
	for i, a := range probe.Assertions {
		for _, field := range []string{"subject", "from", "to"} {
			body, ok := a[field]
			if !ok {
				continue
			}
			var sel Selector
			if err := json.Unmarshal(body, &sel); err != nil {
				continue
			}
			problems = append(problems, checkSelector(sel, fmt.Sprintf("assertions[%d].%s", i, field))...)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("invalid overlay:\n  %s", strings.Join(problems, "\n  "))
}

// Validate checks the invariants the JSON Schema cannot express.
func (d *Document) Validate() error {
	var problems []string

	sinks := map[string]bool{}
	for i, s := range d.Sinks {
		if sinks[s.ID] {
			problems = append(problems, fmt.Sprintf("sinks[%d]: duplicate id %q", i, s.ID))
		}
		sinks[s.ID] = true
	}

	for i, a := range d.Assertions {
		where := fmt.Sprintf("assertions[%d]", i)

		allowed := map[string]bool{}
		for _, f := range alwaysMeaningful {
			allowed[f] = true
		}
		for _, f := range meaningful[a.Assert] {
			allowed[f] = true
		}
		for _, k := range sortedKeys(a.present) {
			if !allowed[k] {
				problems = append(problems, fmt.Sprintf(
					"%s: %q is not meaningful for %s (it takes %s)",
					where, k, a.Assert, strings.Join(meaningful[a.Assert], ", ")))
			}
		}

		if a.Sink != "" && !sinks[a.Sink] {
			problems = append(problems, fmt.Sprintf(
				"%s: sink %q is not declared in this document's sinks", where, a.Sink))
		}

		for name, sel := range map[string]Selector{"subject": a.Subject, "from": a.From, "to": a.To} {
			problems = append(problems, checkSelector(sel, where+"."+name)...)
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("invalid overlay:\n  %s", strings.Join(problems, "\n  "))
}

// structuralKeys are selector keys that are not provider identities.
var structuralKeys = map[string]bool{
	"node": true, "id": true, "group": true, "type": true, "name": true,
}

func checkSelector(s Selector, where string) []string {
	if len(s) == 0 {
		return nil
	}

	var problems []string
	for _, k := range sortedKeys(toSet(s)) {
		if structuralKeys[k] || providers.IsSelectorKey(k) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: unknown selector key %q; known keys are %s",
			where, k, strings.Join(append(sortedStructural(), providers.SelectorKeys()...), ", ")))
	}
	// A namespace with nothing to qualify selects every workload in it, which
	// is never what an author meant and would resolve as ambiguous with a
	// confusing message. Say so here instead.
	if _, ok := s["namespace"]; ok && len(s) == 1 {
		problems = append(problems, where+`: "namespace" on its own names a namespace, not a workload in it; add "workload"`)
	}
	return problems
}

func sortedStructural() []string {
	out := make([]string, 0, len(structuralKeys))
	for k := range structuralKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toSet(s Selector) map[string]bool {
	out := make(map[string]bool, len(s))
	for k := range s {
		out[k] = true
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

// claim resolves an assertion's provenance against the document's defaults.
func (d *Document) claim(a Assertion) core.Claim {
	c := core.Claim{Origin: core.OriginHuman, Note: a.Note}

	if d.Metadata != nil {
		if d.Metadata.Origin != "" {
			c.Origin = d.Metadata.Origin
		}
		c.Author = d.Metadata.Author
	}
	if a.Origin != "" {
		c.Origin = a.Origin
	}
	if a.Author != "" {
		c.Author = a.Author
	}
	c.Confidence = a.Confidence
	return c
}

// window returns the period the author says their observations cover.
func (d *Document) window() string {
	if d.Metadata == nil {
		return ""
	}
	return d.Metadata.Window
}

// sink finds a declared sink by its local handle.
//
// Sink ids are document-local rather than global. The browser export flow
// naturally produces overlay.json, overlay (1).json and so on, and two files
// that both call a handle "sink.app" are not making a claim about each other.
// Scoping them per document means several overlays compose without their
// authors having to coordinate.
func (d *Document) sink(id string) (Sink, bool) {
	for _, s := range d.Sinks {
		if s.ID == id {
			return s, true
		}
	}
	return Sink{}, false
}
