// Package prometheus converts Prometheus text exposition into observations.
// It is a pure parser: scraping, authentication, and query selection stay in
// the caller so the oekaki render path remains credential-free.
package prometheus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imohiyoko/oekaki/core"
)

type Options struct {
	SubjectLabel string
	Unit         string
	ObservedAt   string
	Thresholds   map[string]core.Threshold
}

// Document is the file boundary between a metric collector and the graph
// pipeline. It intentionally contains observations only: credentials and
// endpoint responses do not cross this boundary.
type Document struct {
	Kind         string             `json:"kind"`
	Version      string             `json:"version"`
	GeneratedAt  time.Time          `json:"generated_at"`
	Observations []core.Observation `json:"observations"`
}

type Sink interface {
	Write(context.Context, Document) error
}

// Poller periodically scrapes a Prometheus-compatible endpoint. It is
// deliberately small so the same component can run beside a service or in a
// central collector; the subject label determines which graph node receives
// each sample.
type Poller struct {
	Client   *http.Client
	Endpoint string
	Options  Options
	Sink     Sink
	Clock    func() time.Time
}

func (p *Poller) PollOnce(ctx context.Context) (Document, error) {
	if p.Endpoint == "" || p.Sink == nil {
		return Document{}, fmt.Errorf("prometheus poller requires endpoint and sink")
	}
	now := time.Now()
	if p.Clock != nil {
		now = p.Clock()
	}
	obs, err := Scrape(ctx, p.Client, p.Endpoint, p.Options)
	if err != nil {
		return Document{}, err
	}
	for i := range obs {
		if obs[i].ObservedAt == "" {
			obs[i].ObservedAt = now.UTC().Format(time.RFC3339Nano)
		}
	}
	doc := Document{Kind: "oekaki.observations", Version: "1", GeneratedAt: now, Observations: obs}
	if err := p.Sink.Write(ctx, doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (p *Poller) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		_, err := p.PollOnce(ctx)
		return err
	}
	for {
		if _, err := p.PollOnce(ctx); err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				// A transient metrics endpoint failure must not stop future
				// samples. Callers can inspect their own transport logs.
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// JSONSink accumulates metric history by a deterministic observation key.
// Samples are retained rather than overwritten so the HTML timeline can show
// changes over a polling window.
type JSONSink struct {
	Path string
	Mode os.FileMode
}

var sinkLocks = struct {
	sync.Mutex
	byPath map[string]*sync.Mutex
}{byPath: map[string]*sync.Mutex{}}

func lockForPath(path string) func() {
	key, err := filepath.Abs(path)
	if err != nil {
		key = path
	}
	sinkLocks.Lock()
	mu := sinkLocks.byPath[key]
	if mu == nil {
		mu = &sync.Mutex{}
		sinkLocks.byPath[key] = mu
	}
	sinkLocks.Unlock()
	mu.Lock()
	return mu.Unlock
}

func (s JSONSink) Write(ctx context.Context, doc Document) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if s.Path == "" {
		return fmt.Errorf("observation output path is empty")
	}
	unlock := lockForPath(s.Path)
	defer unlock()
	var old Document
	if raw, err := os.ReadFile(s.Path); err == nil {
		_ = json.Unmarshal(raw, &old)
	}
	merged := map[string]core.Observation{}
	for _, o := range old.Observations {
		merged[observationKey(o)] = o
	}
	for _, o := range doc.Observations {
		merged[observationKey(o)] = o
	}
	doc.Observations = make([]core.Observation, 0, len(merged))
	for _, o := range merged {
		doc.Observations = append(doc.Observations, o)
	}
	sort.SliceStable(doc.Observations, func(i, j int) bool {
		if doc.Observations[i].ObservedAt == doc.Observations[j].ObservedAt {
			return observationKey(doc.Observations[i]) < observationKey(doc.Observations[j])
		}
		return doc.Observations[i].ObservedAt < doc.Observations[j].ObservedAt
	})
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if s.Mode == 0 {
		s.Mode = 0600
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".observations-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(s.Mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.Path)
}

func observationKey(o core.Observation) string {
	labels := make([][2]string, 0, len(o.Labels))
	for key, value := range o.Labels {
		labels = append(labels, [2]string{key, value})
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i][0] != labels[j][0] {
			return labels[i][0] < labels[j][0]
		}
		return labels[i][1] < labels[j][1]
	})
	// Tuple encoding escapes delimiters and gives nil and empty label maps the
	// same non-nil empty slice representation.
	key, _ := json.Marshal(struct {
		Subject    string      `json:"subject"`
		Metric     string      `json:"metric"`
		ObservedAt string      `json:"observed_at"`
		Labels     [][2]string `json:"labels"`
	}{Subject: o.Subject, Metric: o.Metric, ObservedAt: o.ObservedAt, Labels: labels})
	return string(key)
}

// Scrape fetches a Prometheus-compatible endpoint. Authentication belongs to
// the supplied client/transport; this package never reads credentials from
// the graph or logs them.
func Scrape(ctx context.Context, client *http.Client, endpoint string, opts Options) ([]core.Observation, error) {
	if client == nil {
		client = http.DefaultClient
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scraping Prometheus: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("prometheus returned HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading Prometheus response: %w", err)
	}
	return Parse(string(body), opts)
}

func Parse(text string, opts Options) ([]core.Observation, error) {
	if opts.SubjectLabel == "" {
		opts.SubjectLabel = "service"
	}
	var out []core.Observation
	s := bufio.NewScanner(strings.NewReader(text))
	line := 0
	for s.Scan() {
		line++
		raw := strings.TrimSpace(s.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		name, labels, value, err := sample(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		subject := labels[opts.SubjectLabel]
		if subject == "" {
			subject = labels["id"]
		}
		if subject == "" {
			subject = labels["instance"]
		}
		if subject == "" {
			continue
		}
		v := value
		o := core.Observation{Subject: subject, Metric: name, Labels: labels, Value: &v, Unit: opts.Unit, ObservedAt: opts.ObservedAt, Evidence: &core.Claim{Origin: core.OriginParser, Note: "Prometheus exposition"}}
		if threshold, ok := opts.Thresholds[name]; ok {
			t := threshold
			o.Threshold = &t
		}
		out = append(out, o)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sample(line string) (string, map[string]string, float64, error) {
	head, fields, err := splitSample(line)
	if err != nil {
		return "", nil, 0, err
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", nil, 0, fmt.Errorf("invalid value: %w", err)
	}
	labels := map[string]string{}
	name := head
	if i := strings.IndexByte(head, '{'); i >= 0 {
		if !strings.HasSuffix(head, "}") {
			return "", nil, 0, fmt.Errorf("malformed labels")
		}
		name = head[:i]
		body := head[i+1 : len(head)-1]
		for _, part := range splitLabels(body) {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			value := strings.TrimSpace(kv[1])
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
			labels[strings.TrimSpace(kv[0])] = value
		}
	}
	if name == "" {
		return "", nil, 0, fmt.Errorf("empty metric name")
	}
	return name, labels, v, nil
}

func splitSample(line string) (string, []string, error) {
	line = strings.TrimSpace(line)
	end := len(line)
	hasLabels := false
	if i := strings.IndexByte(line, '{'); i >= 0 {
		hasLabels = true
		quote, escaped := false, false
		for j := i + 1; j < len(line); j++ {
			if escaped {
				escaped = false
				continue
			}
			if line[j] == '\\' && quote {
				escaped = true
				continue
			}
			if line[j] == '"' {
				quote = !quote
			}
			if line[j] == '}' && !quote {
				end = j + 1
				break
			}
		}
		if end == len(line) {
			return "", nil, fmt.Errorf("malformed labels")
		}
	}
	if !hasLabels {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return "", nil, fmt.Errorf("sample has no value")
		}
		return parts[0], parts[1:], nil
	}
	parts := strings.Fields(line[end:])
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("sample has no value")
	}
	return strings.TrimSpace(line[:end]), parts, nil
}

func splitLabels(body string) []string {
	var out []string
	start := 0
	quote, escaped := false, false
	for i := 0; i < len(body); i++ {
		if escaped {
			escaped = false
			continue
		}
		if body[i] == '\\' && quote {
			escaped = true
			continue
		}
		if body[i] == '"' {
			quote = !quote
			continue
		}
		if body[i] == ',' && !quote {
			out = append(out, body[start:i])
			start = i + 1
		}
	}
	return append(out, body[start:])
}
