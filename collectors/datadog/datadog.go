// Package datadog converts a Datadog query response into generic
// observations. Callers provide the authenticated HTTP request; credentials
// never enter the graph document.
package datadog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/core"
)

type Response struct {
	Series []Series `json:"series"`
}
type Series struct {
	Metric string              `json:"metric"`
	Scope  string              `json:"scope"`
	Unit   string              `json:"unit"`
	Points [][]json.RawMessage `json:"pointlist"`
}
type Options struct {
	SubjectTag     string
	ObservedAtUnit string
}

func Fetch(ctx context.Context, client *http.Client, req *http.Request, opts Options) ([]core.Observation, error) {
	if req == nil {
		return nil, fmt.Errorf("datadog request is nil")
	}
	if client == nil {
		client = http.DefaultClient
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req = req.WithContext(requestCtx)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Datadog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("datadog returned HTTP %s", resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return Parse(b, opts)
}
func Parse(raw []byte, opts Options) ([]core.Observation, error) {
	if opts.SubjectTag == "" {
		opts.SubjectTag = "service"
	}
	var d Response
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parsing Datadog response: %w", err)
	}
	var out []core.Observation
	for _, s := range d.Series {
		sub := subject(s.Scope, opts.SubjectTag)
		if sub == "" {
			continue
		}
		for _, p := range s.Points {
			if len(p) < 2 {
				continue
			}
			var ts, val float64
			if err := json.Unmarshal(p[0], &ts); err != nil {
				continue
			}
			if err := json.Unmarshal(p[1], &val); err != nil {
				continue
			}
			v := val
			unit := s.Unit
			at := ""
			if opts.ObservedAtUnit == "s" {
				at = strconv.FormatInt(int64(ts), 10)
			}
			out = append(out, core.Observation{Subject: sub, Metric: s.Metric, Value: &v, Unit: unit, ObservedAt: at, Evidence: &core.Claim{Origin: core.OriginParser, Note: "Datadog query"}})
		}
	}
	return out, nil
}
func subject(scope, tag string) string {
	for _, part := range strings.Split(scope, ",") {
		part = strings.TrimSpace(part)
		prefix := tag + ":"
		if strings.HasPrefix(part, prefix) {
			return strings.TrimPrefix(part, prefix)
		}
	}
	return ""
}
