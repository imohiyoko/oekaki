// Package observations applies collector output to an evidence graph.
// Collectors own credentials and API-specific logic; this package only joins
// their stable, file-based output to graph entities.
package observations

import (
	"encoding/json"
	"fmt"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

type Document struct {
	Kind         string             `json:"kind"`
	Version      string             `json:"version"`
	Observations []core.Observation `json:"observations"`
}

func Parse(raw []byte) (*Document, error) {
	var d Document
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parsing observations: %w", err)
	}
	if d.Kind != "oekaki.observations" {
		return nil, fmt.Errorf("invalid observation kind %q", d.Kind)
	}
	if d.Version == "" {
		return nil, fmt.Errorf("observation version is required")
	}
	for i, o := range d.Observations {
		if o.Subject == "" || o.Metric == "" {
			return nil, fmt.Errorf("observations[%d] requires subject and metric", i)
		}
	}
	return &d, nil
}

type Enricher struct{ Docs []*Document }

func (e Enricher) Name() string { return "observations" }

func (e Enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	r := &enrichers.Report{Enricher: e.Name()}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, grp := range g.Groups {
		known[grp.ID] = true
	}
	for _, d := range e.Docs {
		for _, o := range d.Observations {
			if !known[o.Subject] {
				r.Unmatched = append(r.Unmatched, enrichers.Unmatched{Selector: map[string]string{"id": o.Subject}, Assert: "observation", Reason: "subject not found", Action: "reported"})
				continue
			}
			if o.State == "" && o.Threshold != nil && o.Value != nil {
				abnormal, ok := compare(*o.Value, o.Threshold.Operator, o.Threshold.Value)
				if !ok {
					return nil, fmt.Errorf("unknown threshold operator %q", o.Threshold.Operator)
				}
				if abnormal {
					o.State = "abnormal"
					if o.Reason == "" {
						o.Reason = fmt.Sprintf("%s %s threshold %v", o.Metric, o.Threshold.Operator, o.Threshold.Value)
					}
				} else {
					o.State = "normal"
				}
			}
			g.Observations = append(g.Observations, o)
			r.Applied++
		}
	}
	g.Normalize()
	r.Sort()
	return r, nil
}

func compare(value float64, operator string, threshold float64) (bool, bool) {
	switch operator {
	case ">":
		return value > threshold, true
	case ">=":
		return value >= threshold, true
	case "<":
		return value < threshold, true
	case "<=":
		return value <= threshold, true
	case "==":
		return value == threshold, true
	case "!=":
		return value != threshold, true
	default:
		return false, false
	}
}
