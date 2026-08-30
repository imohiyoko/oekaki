package loginventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SQLStore reads a caller-defined query. The query receives the watermark as
// its first argument, typically through `WHERE observed_at > ?`. The caller
// owns DB drivers, credentials, pooling, and transaction policy.
type SQLStore struct {
	DB    *sql.DB
	Query string
	Args  []any
}

func (s SQLStore) Fetch(ctx context.Context, since time.Time) ([]Record, error) {
	if s.DB == nil || s.Query == "" {
		return nil, fmt.Errorf("SQLStore requires DB and Query")
	}
	args := append([]any{since}, s.Args...)
	rows, err := s.DB.QueryContext(ctx, s.Query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.ObservedAt, &r.Source, &r.Body); err != nil {
			return nil, fmt.Errorf("scanning log row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Object is the minimal object contract needed for S3, GCS, or an internal
// object store adapter. Implementations may use SDKs and credentials outside
// this package.
type Object struct {
	Key        string
	ModifiedAt time.Time
	Body       io.ReadCloser
}
type ObjectStore interface {
	List(ctx context.Context, prefix string, since time.Time) ([]Object, error)
}
type ObjectStoreReader struct {
	Store  ObjectStore
	Prefix string
}

func (s ObjectStoreReader) Fetch(ctx context.Context, since time.Time) ([]Record, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("ObjectStoreReader requires Store")
	}
	objects, err := s.Store.List(ctx, s.Prefix, since)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, obj := range objects {
		if obj.Body == nil {
			continue
		}
		dec := json.NewDecoder(obj.Body)
		for {
			var r Record
			if err := dec.Decode(&r); err == io.EOF {
				break
			} else if err != nil {
				obj.Body.Close()
				return nil, fmt.Errorf("decoding object %s: %w", obj.Key, err)
			}
			if r.Source == "" {
				r.Source = obj.Key
			}
			if !r.ObservedAt.After(since) {
				continue
			}
			out = append(out, r)
		}
		obj.Body.Close()
	}
	return out, nil
}

// DirectoryStore is useful for mounted S3 buckets and local replay fixtures.
// Files contain one JSON Record per line and are selected by modification time.
type DirectoryStore struct{ Root string }

func (s DirectoryStore) Fetch(ctx context.Context, since time.Time) ([]Record, error) {
	if s.Root == "" {
		return nil, fmt.Errorf("DirectoryStore requires Root")
	}
	var out []Record
	err := filepath.Walk(s.Root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if info.IsDir() || info.ModTime().Before(since) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(f)
		for {
			var r Record
			if err := dec.Decode(&r); err == io.EOF {
				break
			} else if err != nil {
				f.Close()
				return fmt.Errorf("decoding %s: %w", path, err)
			}
			if r.Source == "" {
				r.Source = strings.TrimPrefix(strings.TrimPrefix(path, s.Root), string(filepath.Separator))
			}
			if !r.ObservedAt.After(since) {
				continue
			}
			out = append(out, r)
		}
		return f.Close()
	})
	return out, err
}

// HTTPJSONStore works for OpenSearch/Calkey gateways that expose a normalized
// JSON array of Records. Authentication, headers, query DSL, and TLS policy
// are all supplied by the caller-owned request/client.
type HTTPJSONStore struct {
	Client  *http.Client
	Request func(context.Context, time.Time) (*http.Request, error)
}

func (s HTTPJSONStore) Fetch(ctx context.Context, since time.Time) ([]Record, error) {
	if s.Request == nil {
		return nil, fmt.Errorf("HTTPJSONStore requires Request")
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := s.Request(ctx, since)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("log API returned HTTP %s", resp.Status)
	}
	var out []Record
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding log API response: %w", err)
	}
	return out, nil
}
