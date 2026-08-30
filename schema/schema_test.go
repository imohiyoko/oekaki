package schema_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/schema"
)

// The schema is the contract a third-party parser has to meet, so the corpus
// under testdata is the thing that actually defines it. Add a file there rather
// than a case here.

func TestValidDocumentsAreAccepted(t *testing.T) {
	for _, path := range corpus(t, "valid") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			doc, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(doc); err != nil {
				t.Errorf("valid document rejected: %v", err)
			}
		})
	}
}

func TestValidDocumentsAlsoDecodeThroughCore(t *testing.T) {
	for _, path := range corpus(t, "valid") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			doc, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := core.Decode(bytes.NewReader(doc)); err != nil {
				t.Errorf("schema-valid document rejected by core: %v", err)
			}
		})
	}
}

func TestInvalidDocumentsAreRejected(t *testing.T) {
	for _, path := range corpus(t, "invalid") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			doc, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(doc); err == nil {
				t.Error("invalid document accepted")
			}
		})
	}
}

func TestSchemaCompiles(t *testing.T) {
	if _, err := schema.Compiled(); err != nil {
		t.Fatalf("the embedded schema does not compile: %v", err)
	}
	if err := schema.ValidateAICandidates([]byte(`{"kind":"oekaki.ai-candidates","version":"1","nodes":[{"id":"x","type":"service","name":"x"}],"candidates":[]}`)); err != nil {
		t.Fatalf("the embedded AI candidate schema rejected a valid document: %v", err)
	}
	if err := schema.ValidateAICandidates([]byte(`{"kind":"oekaki.ai-candidates","version":"1","needs":[{"kind":"repository","identifier":"payments","reason":"not supplied"}]}`)); err != nil {
		t.Fatalf("the embedded AI needs schema rejected a valid document: %v", err)
	}
	if err := schema.ValidateReachability([]byte(`{"kind":"oekaki.reachability","version":"1","paths":[{"from":"a","to":"b","allowed":true}]}`)); err != nil {
		t.Fatalf("the embedded reachability schema rejected a valid document: %v", err)
	}
	if err := schema.ValidateReachability([]byte(`{"kind":"oekaki.reachability","version":"1","paths":[{"from":"a","to":"b","port":65536,"allowed":true}]}`)); err == nil {
		t.Fatal("reachability schema accepted an invalid port")
	}
	if err := schema.ValidateObservations([]byte(`{"kind":"oekaki.observations","version":"1","observations":[{"subject":"a","metric":"temperature","labels":{"sensor":"room-1"},"value":23.5}]}`)); err != nil {
		t.Fatalf("the embedded observations schema rejected a valid document: %v", err)
	}
}

func TestValidateReportsBadJSON(t *testing.T) {
	if err := schema.Validate([]byte("{not json")); err == nil {
		t.Fatal("expected malformed JSON to be reported")
	}
}

func corpus(t *testing.T, kind string) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("testdata", kind, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no %s fixtures found", kind)
	}
	return paths
}
