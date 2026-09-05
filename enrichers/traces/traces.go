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

		// The routes those spans walked, and how often. A route is reported
		// unmatched as a whole rather than trimmed down to the participants
		// this graph happens to know: a walk with a hop removed is a
		// different walk, and one silently repaired here would be compared
		// against the declared set as though somebody had observed it.
		paths, counts := d.Paths()
		for i, path := range paths {
			missing := ""
			for _, id := range path.Nodes {
				if !known[id] {
					missing = id
					break
				}
			}
			if missing != "" {
				r.Unmatched = append(r.Unmatched, enrichers.Unmatched{
					Selector: map[string]string{"path": core.PathKey(path.Nodes)},
					Assert:   "path", Reason: "participant " + missing + " not found", Action: "reported",
				})
				continue
			}
			g.Paths = append(g.Paths, path)
			g.Observations = append(g.Observations, counts[i])
			r.Applied++
		}
	}
	g.Normalize()
	r.Sort()
	return r, nil
}
