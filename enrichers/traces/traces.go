package traces

import (
	"github.com/imohiyoko/oekaki/collectors/traces"
	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

type Enricher struct{ Documents []*traces.Document }

func (Enricher) Name() string { return "traces" }
func (e Enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	r := &enrichers.Report{Enricher: e.Name()}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, d := range e.Documents {
		for _, edge := range d.Edges() {
			if !known[edge.From] || !known[edge.To] {
				r.Unmatched = append(r.Unmatched, enrichers.Unmatched{Selector: map[string]string{"from": edge.From, "to": edge.To}, Assert: "trace", Reason: "service endpoint not found", Action: "reported"})
				continue
			}
			g.Edges = append(g.Edges, edge)
			r.Applied++
		}
		for _, s := range d.Spans {
			if known[s.Service] {
				g.Observations = append(g.Observations, s.Observation())
			}
		}
	}
	g.Normalize()
	r.Sort()
	return r, nil
}
