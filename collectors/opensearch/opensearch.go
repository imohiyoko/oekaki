// Package opensearch parses the standard OpenSearch _search JSON response
// into log records. Request construction and authentication stay with the
// caller, so API keys are never part of an inventory or graph.
package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/imohiyoko/oekaki/collectors/loginventory"
)

type Response struct {
	Hits Hits `json:"hits"`
}
type Hits struct {
	Hits []Hit `json:"hits"`
}
type Hit struct {
	ID     string `json:"_id"`
	Source Source `json:"_source"`
}
type Source struct {
	ID         string            `json:"id"`
	Service    string            `json:"service"`
	Source     string            `json:"source"`
	ObservedAt string            `json:"observed_at"`
	Body       string            `json:"message"`
	Attributes map[string]string `json:"attributes"`
}

// Fetch executes a caller-constructed OpenSearch request and parses its
// response. The client, request headers, credentials, query, and TLS policy
// remain owned by the caller and never enter an inventory or graph.
func Fetch(ctx context.Context, client *http.Client, req *http.Request) ([]loginventory.Record, error) {
	if req == nil {
		return nil, fmt.Errorf("OpenSearch request is nil")
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("fetching OpenSearch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenSearch returned HTTP %s", resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading OpenSearch response: %w", err)
	}
	return Parse(raw)
}

func Parse(raw []byte) ([]loginventory.Record, error) {
	var d Response
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parsing OpenSearch response: %w", err)
	}
	out := make([]loginventory.Record, 0, len(d.Hits.Hits))
	for _, h := range d.Hits.Hits {
		id := h.Source.ID
		if id == "" {
			id = h.ID
		}
		src := h.Source.Source
		if src == "" {
			src = h.Source.Service
		}
		out = append(out, loginventory.Record{ID: id, Source: src, ObservedAt: parseTime(h.Source.ObservedAt), Body: h.Source.Body, Attributes: h.Source.Attributes})
	}
	return out, nil
}
func parseTime(s string) (t time.Time) {
	if s == "" {
		return
	}
	t, _ = time.Parse(time.RFC3339Nano, s)
	return
}
