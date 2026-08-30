// Package exposure applies a credential-free security exposure report.
// A collector should obtain cloud/network state and write this document; the
// graph process only joins the report and records its provenance.
package exposure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/enrichers"
)

type Document struct {
	Kind     string    `json:"kind"`
	Version  string    `json:"version"`
	Findings []Finding `json:"findings"`
}

type Finding struct {
	Subject  string      `json:"subject"`
	Endpoint string      `json:"endpoint,omitempty"`
	Protocol string      `json:"protocol,omitempty"`
	Port     int         `json:"port,omitempty"`
	Public   bool        `json:"public"`
	State    string      `json:"state,omitempty"`
	Reason   string      `json:"reason,omitempty"`
	Claim    *core.Claim `json:"claim,omitempty"`
}

func (f *Finding) UnmarshalJSON(data []byte) error {
	type finding struct {
		Subject  string      `json:"subject"`
		Endpoint string      `json:"endpoint,omitempty"`
		Protocol string      `json:"protocol,omitempty"`
		Port     int         `json:"port,omitempty"`
		Public   *bool       `json:"public"`
		State    string      `json:"state,omitempty"`
		Reason   string      `json:"reason,omitempty"`
		Claim    *core.Claim `json:"claim,omitempty"`
	}
	var decoded finding
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Public == nil {
		return fmt.Errorf("public is required")
	}
	*f = Finding{
		Subject: decoded.Subject, Endpoint: decoded.Endpoint, Protocol: decoded.Protocol,
		Port: decoded.Port, Public: *decoded.Public, State: decoded.State,
		Reason: decoded.Reason, Claim: decoded.Claim,
	}
	return nil
}

func Parse(raw []byte) (*Document, error) {
	var d Document
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parsing exposure report: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return nil, fmt.Errorf("parsing exposure report: %w", err)
	}
	if d.Kind != "oekaki.exposure" {
		return nil, fmt.Errorf("invalid exposure kind %q", d.Kind)
	}
	if d.Version == "" {
		return nil, fmt.Errorf("exposure version is required")
	}
	for i, f := range d.Findings {
		if f.Subject == "" {
			return nil, fmt.Errorf("findings[%d].subject is required", i)
		}
	}
	return &d, nil
}

func ensureEOF(dec *json.Decoder) error {
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

type Enricher struct{ Docs []*Document }

func (e Enricher) Name() string { return "exposure" }

func (e Enricher) Enrich(g *core.Graph) (*enrichers.Report, error) {
	r := &enrichers.Report{Enricher: e.Name()}
	known := map[string]bool{}
	for _, n := range g.Nodes {
		known[n.ID] = true
	}
	for _, f := range g.Groups {
		known[f.ID] = true
	}
	for _, d := range e.Docs {
		for _, f := range d.Findings {
			if !known[f.Subject] {
				r.Unmatched = append(r.Unmatched, enrichers.Unmatched{Selector: map[string]string{"id": f.Subject}, Assert: "exposure", Reason: "subject not found", Action: "reported"})
				continue
			}
			state := f.State
			if state == "" {
				if f.Public {
					state = "abnormal"
				} else {
					state = "normal"
				}
			}
			v := 0.0
			if f.Public {
				v = 1
			}
			g.Observations = append(g.Observations, core.Observation{Subject: f.Subject, Metric: "external_exposure", Value: &v, Unit: "boolean", State: state, Reason: f.Reason, Evidence: f.Claim})
			if f.Public {
				ensureInternet(g)
				attrs := map[string]any{"endpoint": f.Endpoint, "protocol": f.Protocol, "port": f.Port}
				g.Edges = append(g.Edges, core.Edge{From: "external:internet", To: f.Subject, Kind: core.EdgeReachable, Relation: "exposes", Attrs: attrs, Claim: f.Claim})
			}
			r.Applied++
		}
	}
	g.Normalize()
	r.Sort()
	return r, nil
}

func ensureInternet(g *core.Graph) {
	for _, n := range g.Nodes {
		if n.ID == "external:internet" {
			return
		}
	}
	g.Nodes = append(g.Nodes, core.Node{ID: "external:internet", Type: "external_endpoint", Name: "Internet", Provider: "external"})
}
