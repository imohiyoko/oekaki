package schema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/schema"
)

func TestLayoutCorpus(t *testing.T) {
	valid, _ := filepath.Glob("testdata/layout/valid/*.json")
	invalid, _ := filepath.Glob("testdata/layout/invalid/*.json")
	if len(valid) == 0 || len(invalid) == 0 {
		t.Fatal("layout corpus is empty")
	}
	for _, path := range valid {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.ValidateLayout(raw); err != nil {
			t.Errorf("valid %s rejected: %v", path, err)
		}
		if !schema.IsLayout(raw) {
			t.Errorf("%s not recognised as layout", path)
		}
	}
	for _, path := range invalid {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.ValidateLayout(raw); err == nil {
			t.Errorf("invalid %s accepted", path)
		}
	}
	if _, err := schema.CompiledLayout(); err != nil {
		t.Fatal(err)
	}
	if err := schema.ValidateLayout([]byte("{not json")); err == nil || !strings.Contains(err.Error(), "parsing document") {
		t.Fatal("bad JSON not reported")
	}
}
