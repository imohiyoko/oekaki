// Package traces converts exported request spans into graph evidence.
package traces

import (
	"encoding/json"
	"fmt"
	"github.com/imohiyoko/oekaki/core"
	"time"
)

type Span struct {
	TraceID string `json:"trace_id"`

	// SpanID and ParentSpanID are this span's own identity, when the exporter
	// records it. They are optional because the documents people already have
	// do not always carry them — but without them a service that two callers
	// reach cannot be placed in the tree, and the routes through it cannot be
	// recovered. See Document.Paths.
	SpanID       string `json:"span_id,omitempty"`
	ParentSpanID string `json:"parent_span_id,omitempty"`

	Service       string  `json:"service"`
	ParentService string  `json:"parent_service,omitempty"`
	Operation     string  `json:"operation,omitempty"`
	DurationMS    float64 `json:"duration_ms,omitempty"`
	Status        string  `json:"status,omitempty"`
	ObservedAt    string  `json:"observed_at,omitempty"`
}
type Document struct {
	Version string `json:"version"`
	Spans   []Span `json:"spans"`
}

func Parse(raw []byte) (*Document, error) {
	var d Document
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parsing trace document: %w", err)
	}
	if d.Version == "" {
		return nil, fmt.Errorf("trace document version is required")
	}
	for i, s := range d.Spans {
		if s.Service == "" {
			return nil, fmt.Errorf("spans[%d].service is required", i)
		}
	}
	return &d, nil
}
func (d *Document) Edges() []core.Edge {
	seen := map[string]bool{}
	out := []core.Edge{}
	for _, s := range d.Spans {
		if s.ParentService == "" || s.ParentService == s.Service {
			continue
		}
		from, to := s.ParentService, s.Service
		key := from + "|" + to + "|calls"
		if seen[key] {
			continue
		}
		seen[key] = true
		attrs := map[string]any{"trace_id": s.TraceID, "operation": s.Operation}
		if s.DurationMS != 0 {
			attrs["duration_ms"] = s.DurationMS
		}
		out = append(out, core.Edge{From: from, To: to, Kind: core.EdgeObserved, Relation: "calls", Attrs: attrs})
	}
	return out
}
func (s Span) Observation() core.Observation {
	v := s.DurationMS
	return core.Observation{Subject: s.Service, Metric: "request_duration", Value: &v, Unit: "ms", ObservedAt: normalizeTime(s.ObservedAt), State: statusState(s.Status), Evidence: &core.Claim{Origin: core.OriginParser, Note: "request trace span"}}
}
func normalizeTime(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func statusState(s string) string {
	switch s {
	case "error", "ERROR", "failed", "FAILED":
		return "abnormal"
	default:
		return "normal"
	}
}
