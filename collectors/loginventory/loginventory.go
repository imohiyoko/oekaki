// Package loginventory provides the polling boundary for log collection.
//
// A Store may be backed by S3, SQL, OpenSearch, or an internal log service.
// The poller never assumes a backend and never persists raw log bodies: it
// records stable IDs, selected characteristics, and labels only.
package loginventory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

type Record struct {
	ID         string            `json:"id"`
	Source     string            `json:"source,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
	Body       string            `json:"-"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// UnmarshalJSON accepts the raw body at the collector boundary while keeping
// Body decode-only. Record deliberately has no matching MarshalJSON method:
// the json:"-" tag above prevents raw log content from being written if a
// caller accidentally serializes a Record.
func (r *Record) UnmarshalJSON(data []byte) error {
	type record Record
	decoded := struct {
		*record
		Body string `json:"body"`
	}{record: (*record)(r)}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	r.Body = decoded.Body
	return nil
}

type ClassifiedRecord struct {
	ID              string            `json:"id"`
	Source          string            `json:"source,omitempty"`
	ObservedAt      time.Time         `json:"observed_at"`
	Characteristics map[string]string `json:"characteristics,omitempty"`
	Labels          []string          `json:"labels,omitempty"`
}

// Store is implemented by backend-specific readers. Since is a watermark
// supplied by the poller; a backend may use it as a timestamp, partition key,
// or search_after boundary.
type Store interface {
	Fetch(ctx context.Context, since time.Time) ([]Record, error)
}
type Classifier interface {
	Classify(Record) (map[string]string, []string, error)
}
type Sink interface {
	Write(ctx context.Context, inventory Inventory) error
}

type Inventory struct {
	Version     string             `json:"version"`
	GeneratedAt time.Time          `json:"generated_at"`
	Status      *PollStatus        `json:"status,omitempty"`
	Records     []ClassifiedRecord `json:"records"`
}

// PollStatus describes acquisition health without retaining backend details or
// raw log data. It is intentionally part of the inventory artifact so a
// dashboard can distinguish "no records" from "the poller never succeeded".
type PollStatus struct {
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Fetched     int       `json:"fetched"`
	Classified  int       `json:"classified"`
	LastError   string    `json:"last_error,omitempty"`
}

type Poller struct {
	Store      Store
	Classifier Classifier
	Sink       Sink
	Clock      func() time.Time
	mu         sync.Mutex
	since      time.Time
	status     PollStatus
}

// Status returns the latest in-memory acquisition status.
func (p *Poller) Status() PollStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *Poller) PollOnce(ctx context.Context) (Inventory, error) {
	if p.Store == nil || p.Classifier == nil || p.Sink == nil {
		return Inventory{}, fmt.Errorf("log poller requires Store, Classifier, and Sink")
	}
	p.mu.Lock()
	since := p.since
	started := time.Now()
	if p.Clock != nil {
		started = p.Clock()
	}
	p.mu.Unlock()
	status := PollStatus{StartedAt: started}
	records, err := p.Store.Fetch(ctx, since)
	if err != nil {
		p.recordFailure(ctx, status, err)
		return Inventory{}, err
	}
	status.Fetched = len(records)
	inv := Inventory{Version: "1", Records: make([]ClassifiedRecord, 0, len(records))}
	for _, r := range records {
		if r.ID == "" {
			r.ID = derivedID(r)
		}
		chars, labels, err := p.Classifier.Classify(r)
		if err != nil {
			wrapped := fmt.Errorf("classifying %s: %w", r.ID, err)
			p.recordFailure(ctx, status, wrapped)
			return Inventory{}, wrapped
		}
		inv.Records = append(inv.Records, ClassifiedRecord{ID: r.ID, Source: r.Source, ObservedAt: r.ObservedAt, Characteristics: chars, Labels: labels})
	}
	sort.SliceStable(inv.Records, func(i, j int) bool {
		if inv.Records[i].ObservedAt.Equal(inv.Records[j].ObservedAt) {
			return inv.Records[i].ID < inv.Records[j].ID
		}
		return inv.Records[i].ObservedAt.Before(inv.Records[j].ObservedAt)
	})
	now := time.Now()
	if p.Clock != nil {
		now = p.Clock()
	}
	status.CompletedAt = now
	status.Classified = len(inv.Records)
	inv.Status = &status
	inv.GeneratedAt = now
	if err := p.Sink.Write(ctx, inv); err != nil {
		p.recordFailure(ctx, status, err)
		return Inventory{}, err
	}
	latest := since
	for _, r := range records {
		if r.ObservedAt.After(latest) {
			latest = r.ObservedAt
		}
	}
	p.mu.Lock()
	p.since = latest
	p.status = status
	p.mu.Unlock()
	return inv, nil
}

func (p *Poller) setStatus(status PollStatus) {
	p.mu.Lock()
	p.status = status
	p.mu.Unlock()
}

func (p *Poller) recordFailure(ctx context.Context, status PollStatus, err error) {
	status.LastError = err.Error()
	if p.Clock != nil {
		status.CompletedAt = p.Clock()
	} else {
		status.CompletedAt = time.Now()
	}
	p.setStatus(status)
	// Best effort: if the sink itself is healthy, persist the failure while its
	// JSON implementation merges the prior records. A sink failure must remain
	// the original error and must not be hidden by this status write.
	if p.Sink != nil {
		_ = p.Sink.Write(ctx, Inventory{Version: "1", GeneratedAt: status.CompletedAt, Status: &status})
	}
}

// derivedID gives records from plain JSONL/object logs a stable identity when
// the source did not provide one. The body is hashed only for identity; it is
// never written to Inventory or Graph.
func derivedID(r Record) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s", r.Source, r.ObservedAt.UTC().Format(time.RFC3339Nano), r.Body)
	return fmt.Sprintf("derived:%x", h.Sum(nil)[:16])
}

func (p *Poller) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		_, err := p.PollOnce(ctx)
		return err
	}
	for {
		// A backend outage is itself persisted in PollStatus. Keep the
		// polling loop alive so a transient outage does not turn a useful
		// long-running collector into a one-shot process.
		_, _ = p.PollOnce(ctx)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

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

func (s JSONSink) Write(ctx context.Context, inv Inventory) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if s.Path == "" {
		return fmt.Errorf("inventory output path is empty")
	}
	unlock := lockForPath(s.Path)
	defer unlock()
	// Inventory is an accumulating artifact. A process restart must not erase
	// the preceding polling window, and a backend replay must not duplicate a
	// record whose ID is stable.
	if existing, err := os.ReadFile(s.Path); err == nil {
		var old Inventory
		if json.Unmarshal(existing, &old) == nil {
			merged := make(map[string]ClassifiedRecord, len(old.Records)+len(inv.Records))
			for _, r := range old.Records {
				merged[r.ID] = r
			}
			for _, r := range inv.Records {
				merged[r.ID] = r
			}
			inv.Records = inv.Records[:0]
			for _, r := range merged {
				inv.Records = append(inv.Records, r)
			}
			sort.SliceStable(inv.Records, func(i, j int) bool {
				if inv.Records[i].ObservedAt.Equal(inv.Records[j].ObservedAt) {
					return inv.Records[i].ID < inv.Records[j].ID
				}
				return inv.Records[i].ObservedAt.Before(inv.Records[j].ObservedAt)
			})
		}
	}
	b, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	if s.Mode == 0 {
		s.Mode = 0600
	}
	dir := filepath.Dir(s.Path)
	tmp, err := os.CreateTemp(dir, ".log-inventory-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
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
	return os.Rename(tmpName, s.Path)
}

// RuleClassifier applies deterministic regular-expression rules to a selected
// log body. Callers decide which fields may be inspected; raw content never
// leaves Classify unless a caller explicitly stores a derived characteristic.
type Rule struct {
	Label           string
	Pattern         *regexp.Regexp
	Characteristics map[string]string
}
type RuleClassifier struct{ Rules []Rule }

func (c RuleClassifier) Classify(r Record) (map[string]string, []string, error) {
	chars := map[string]string{}
	labels := map[string]string{}
	for _, rule := range c.Rules {
		if rule.Pattern == nil || !rule.Pattern.MatchString(r.Body) {
			continue
		}
		for k, v := range rule.Characteristics {
			chars[k] = v
		}
		if rule.Label != "" {
			labels[rule.Label] = rule.Label
		}
	}
	out := make([]string, 0, len(labels))
	for l := range labels {
		out = append(out, l)
	}
	sort.Strings(out)
	return chars, out, nil
}
