// Package layout reads human-authored positions without changing the graph IR.
package layout

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imohiyoko/oekaki/schema"
)

type Document struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Nodes   []Node `json:"nodes"`
	Lines   string `json:"lines,omitempty"`
	Edges   []Edge `json:"edges,omitempty"`
	Claim   Claim  `json:"claim"`
	Source  string `json:"-"`
}

type Node struct {
	ID     string  `json:"id"`
	Parent string  `json:"parent,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	// Width and Height are the size a hand gave the box. Absent means the one
	// the view works out, which for width is what the name needs and for
	// height is the same for every box.
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`
}

// Edge names the side of a box a line leaves and the side it arrives on. It is
// named the way an overlay names an edge — from, to, kind and relation — so
// the same line is called the same thing in both documents.
//
// A side that is missing is chosen from where the two boxes ended up. Where
// along the side the line lands is never given here: lines that share a side
// are spread along it, and a number written down for one of them would be
// wrong as soon as another arrived.
type Edge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Relation string `json:"relation,omitempty"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target,omitempty"`
	Line     string `json:"line,omitempty"`
}

type Claim struct {
	Origin string `json:"origin"`
	Author string `json:"author,omitempty"`
	Note   string `json:"note,omitempty"`
}

func Parse(raw []byte, source string) (*Document, error) {
	if err := schema.ValidateLayout(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	var doc Document
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	seen := make(map[string]bool, len(doc.Nodes))
	for i, n := range doc.Nodes {
		if seen[n.ID] {
			return nil, fmt.Errorf("%s: nodes[%d]: duplicate id %q", source, i, n.ID)
		}
		seen[n.ID] = true
	}
	// Two entries for one line contradict each other rather than add up, and
	// which one wins would come down to the order they happen to be written in.
	edges := make(map[string]bool, len(doc.Edges))
	for i, e := range doc.Edges {
		key := strings.Join([]string{e.From, e.To, e.Kind, e.Relation}, "\x00")
		if edges[key] {
			return nil, fmt.Errorf("%s: edges[%d]: duplicate line %s -> %s (%s)", source, i, e.From, e.To, e.Kind)
		}
		edges[key] = true
	}
	doc.Source = source
	return &doc, nil
}

// Placement is how much of a document a graph can use.
//
// A layout applies by id: the view places the nodes it recognises and leaves
// the rest alone. That is the right behaviour — a graph that grew a node
// should still draw — but it means a document written for a different graph
// still applies, just less of it. Nothing about the drawing says so, which is
// why the count exists.
type Placement struct {
	Placed  int
	Missing []string // ids the graph does not have, in document order
}

// Total is how many positions the document carries.
func (p Placement) Total() int { return p.Placed + len(p.Missing) }

// Against reports how much of the document lands on the given ids.
//
// The caller decides what counts as known — a graph's nodes, its groups, or
// both. A layout may place a container, so both is usually right.
func (d *Document) Against(known map[string]struct{}) Placement {
	var out Placement
	for _, n := range d.Nodes {
		if _, ok := known[n.ID]; ok {
			out.Placed++
		} else {
			out.Missing = append(out.Missing, n.ID)
		}
	}
	return out
}
