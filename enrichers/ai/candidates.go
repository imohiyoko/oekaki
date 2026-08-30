// Package ai is the deterministic boundary for model-produced graph
// candidates. It does not call a model. An external model adapter writes this
// document; Apply validates identities and preserves origin/confidence.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

// Generate invokes an explicitly selected local model adapter. The graph is
// sent to stdin and stdout must be an oekaki AI-candidate document. No
// shell is used; the caller controls whether the executable is local or a
// remote client.
func Generate(ctx context.Context, executable string, args []string, g *core.Graph) (*Document, error) {
	if executable == "" {
		return nil, fmt.Errorf("AI executable is empty")
	}
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	raw, err := g.MarshalIndent()
	if err != nil {
		return nil, fmt.Errorf("encoding graph for AI: %w", err)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running AI candidate generator: %w", err)
	}
	return Parse(out)
}

type Document struct {
	Kind       string      `json:"kind"`
	Version    string      `json:"version"`
	Nodes      []Node      `json:"nodes,omitempty"`
	Candidates []Candidate `json:"candidates"`
	Needs      []Need      `json:"needs,omitempty"`
}

// Node is a deliberately small model-produced declaration. Attributes are
// excluded so a model cannot smuggle arbitrary infrastructure data into the
// graph; details can be added later by a parser or a human assertion.
type Node struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider,omitempty"`
}
type Candidate struct {
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Relation   string   `json:"relation"`
	Confidence *float64 `json:"confidence,omitempty"`
	Note       string   `json:"note,omitempty"`
}

// Need is a model's explicit request for more context. The draw pipeline does
// not fetch it: the operator decides what is safe to clone or mount, then
// supplies the requested path with --repo. Keeping the request structured
// lets a UI offer a repository picker instead of parsing prose.
type Need struct {
	Kind           string   `json:"kind"`
	Identifier     string   `json:"identifier"`
	Reason         string   `json:"reason"`
	RepositoryHint string   `json:"repository_hint,omitempty"`
	References     []string `json:"references,omitempty"`
}

func Parse(raw []byte) (*Document, error) {
	var d Document
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parsing AI candidates: %w", err)
	}
	if d.Kind != "oekaki.ai-candidates" {
		return nil, fmt.Errorf("invalid AI candidate kind %q", d.Kind)
	}
	if d.Version == "" {
		return nil, fmt.Errorf("AI candidate version is required")
	}
	for i, n := range d.Nodes {
		if n.ID == "" || n.Type == "" || n.Name == "" {
			return nil, fmt.Errorf("nodes[%d] requires id, type, name", i)
		}
	}
	for i, c := range d.Candidates {
		if c.From == "" || c.To == "" || c.Relation == "" {
			return nil, fmt.Errorf("candidates[%d] requires from, to, relation", i)
		}
		if c.Confidence != nil && (*c.Confidence < 0 || *c.Confidence > 1) {
			return nil, fmt.Errorf("candidates[%d].confidence must be 0..1", i)
		}
	}
	for i, n := range d.Needs {
		if n.Kind == "" || n.Identifier == "" || n.Reason == "" {
			return nil, fmt.Errorf("needs[%d] requires kind, identifier, reason", i)
		}
	}
	return &d, nil
}

type Enricher struct{ Docs []*Document }

func (e Enricher) Name() string { return "ai-candidates" }
func (e Enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	r := &enrichers.Report{Enricher: e.Name()}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, x := range g.Groups {
		known[x.ID] = true
	}
	for _, d := range e.Docs {
		for _, n := range d.Nodes {
			if known[n.ID] {
				continue
			}
			g.Nodes = append(g.Nodes, core.Node{ID: n.ID, Type: n.Type, Name: n.Name, Description: n.Description, Provider: n.Provider, Claim: &core.Claim{Origin: core.OriginAI}})
			known[n.ID] = true
			r.Applied++
		}
		for _, c := range d.Candidates {
			if !known[c.From] || !known[c.To] {
				r.Unmatched = append(r.Unmatched, enrichers.Unmatched{Selector: map[string]string{"from": c.From, "to": c.To}, Assert: "candidate", Reason: "endpoint not found", Action: "reported"})
				continue
			}
			cl := &core.Claim{Origin: core.OriginAI, Confidence: c.Confidence, Note: c.Note}
			g.Edges = append(g.Edges, core.Edge{From: c.From, To: c.To, Kind: core.EdgeObserved, Relation: c.Relation, Claim: cl})
			r.Applied++
		}
	}
	g.Normalize()
	r.Sort()
	return r, nil
}
