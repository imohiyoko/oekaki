// Package loginventory joins classified log inventory records to graph nodes.
package loginventory

import (
	"fmt"
	"sort"
	"time"

	"github.com/imohiyoko/oekaki/collectors/loginventory"
	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

type Enricher struct{ Inventory loginventory.Inventory }

func (Enricher) Name() string { return "log-inventory" }
func (e Enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	r := &enrichers.Report{Enricher: e.Name()}
	if e.Inventory.Status != nil {
		g.LogStatus = &core.LogCollectionStatus{
			StartedAt:   statusTime(e.Inventory.Status.StartedAt),
			CompletedAt: statusTime(e.Inventory.Status.CompletedAt),
			Fetched:     e.Inventory.Status.Fetched,
			Classified:  e.Inventory.Status.Classified,
			LastError:   e.Inventory.Status.LastError,
		}
	}
	known := map[string]bool{}
	counts := map[string]float64{}
	existingRecords := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, x := range g.Groups {
		known[x.ID] = true
	}
	for _, record := range g.LogRecords {
		existingRecords[record.ID] = true
	}
	for _, rec := range e.Inventory.Records {
		subject, candidates := resolveSource(g, rec.Source, known)
		if len(candidates) > 1 {
			r.Ambiguous = append(r.Ambiguous, enrichers.Ambiguous{Selector: map[string]string{"source": rec.Source}, Assert: "log_record", Candidates: candidates})
			continue
		}
		if subject == "" {
			r.Unmatched = append(r.Unmatched, enrichers.Unmatched{Selector: map[string]string{"id": rec.Source}, Assert: "log_record", Reason: "source node not found", Action: "reported"})
			continue
		}
		if existingRecords[rec.ID] {
			continue
		}
		at := rec.ObservedAt.UTC().Format(time.RFC3339Nano)
		g.LogRecords = append(g.LogRecords, core.LogRecordSummary{ID: rec.ID, Source: subject, ObservedAt: at, Characteristics: rec.Characteristics, Labels: rec.Labels})
		existingRecords[rec.ID] = true
		counts[subject]++
		r.Applied++
	}
	for subject, count := range counts {
		v := count
		g.Observations = append(g.Observations, core.Observation{Subject: subject, Metric: "log_records", Value: &v, Unit: "records", State: "flowing", Evidence: &core.Claim{Origin: core.OriginParser, Note: "classified log inventory"}})
		if node, ok := g.Node(subject); ok && (node.Coverage == nil || node.Coverage.State == core.CoverageUnknown) {
			records := count
			node.Coverage = &core.Coverage{
				State:  core.CoverageFlowing,
				Reason: "classified records were observed for this source",
				Evidence: []core.Evidence{{
					Kind:    core.EvidenceObserved,
					Records: &records,
					Stream:  subject,
					Via:     "classified log inventory",
				}},
			}
		}
	}
	g.Normalize()
	r.Sort()
	return r, nil
}

func statusTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func resolveSource(g *core.Graph, source string, known map[string]bool) (string, []string) {
	if known[source] {
		return source, nil
	}
	var candidates []string
	for _, n := range g.Nodes {
		if n.Name == source || attrContains(n.Attrs, source) {
			candidates = append(candidates, n.ID)
		}
	}
	for _, group := range g.Groups {
		if group.Label == source || attrContains(group.Attrs, source) {
			candidates = append(candidates, group.ID)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 1 {
		return candidates[0], candidates
	}
	return "", candidates
}

func attrContains(attrs map[string]any, wanted string) bool {
	for _, value := range attrs {
		if s, ok := value.(string); ok && s == wanted {
			return true
		}
	}
	return false
}
func ValidateInventory(inv loginventory.Inventory) error {
	if inv.Version == "" {
		return fmt.Errorf("log inventory version is required")
	}
	return nil
}
