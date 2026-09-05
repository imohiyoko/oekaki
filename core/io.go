package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/imohiyoko/oekaki/schema"
)

// Decode reads an IR document. It normalizes and validates, so a graph that
// comes back from Decode is safe for renderers to walk without re-checking.
func Decode(r io.Reader) (*Graph, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading graph: %w", err)
	}
	var g Graph
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("parsing graph: %w", err)
	}
	// Preserve Validate's actionable version-mismatch message instead of
	// replacing it with the schema validator's terse const violation. Two
	// older versions are still read: 0.4, whose untyped conflict targets are
	// resolved against the graph, and 0.5, which differs from the current
	// shape only by not having paths.
	switch version := g.Version; version {
	case legacyV04, legacyV05:
		// Validate the original bytes before migration. Re-encoding a typed
		// Graph first would omit explicit empty legacy fields and could turn
		// an invalid old document into an apparently valid current one.
		if err := schema.ValidateLegacyGraph(version, raw); err != nil {
			return nil, err
		}
		if version == legacyV04 {
			if err := g.migrateLegacyConflictTargets(); err != nil {
				return nil, fmt.Errorf("migrating IR %s to %s: %w", version, Version, err)
			}
		}
		g.Version = Version
		migrated, err := json.Marshal(&g)
		if err != nil {
			return nil, fmt.Errorf("encoding migrated graph: %w", err)
		}
		raw = migrated
	case Version:
		// Current documents are validated below without migration.
	default:
		g.Normalize()
		return nil, g.Validate()
	}
	if err := schema.Validate(raw); err != nil {
		return nil, err
	}

	g.Normalize()
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return &g, nil
}

// Encode writes an IR document as indented JSON with a trailing newline, so it
// reads well in a pull request. It normalizes first: the whole point of the
// canonical ordering is that the file on disk is stable.
func Encode(w io.Writer, g *Graph) error {
	if g.Version != Version {
		return fmt.Errorf("writing graph: version is %q, want %q; Decode legacy input before encoding it", g.Version, Version)
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		return fmt.Errorf("writing graph: %w", err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(g); err != nil {
		return fmt.Errorf("writing graph: %w", err)
	}
	if err := schema.Validate(buf.Bytes()); err != nil {
		return fmt.Errorf("writing graph: %w", err)
	}
	if _, err := buf.WriteTo(w); err != nil {
		return fmt.Errorf("writing graph: %w", err)
	}
	return nil
}

// MarshalJSON renders a graph to the same bytes Encode would write.
func (g *Graph) MarshalIndent() ([]byte, error) {
	var buf bytes.Buffer
	if err := Encode(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
