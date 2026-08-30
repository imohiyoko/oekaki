package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

const CatalogID = "https://raw.githubusercontent.com/imohiyoko/oekaki/main/schema/catalog.schema.json"

//go:embed catalog.schema.json
var CatalogSchema []byte

var compileCatalog = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(CatalogID, bytes.NewReader(CatalogSchema)); err != nil {
		return nil, fmt.Errorf("registering embedded catalog schema: %w", err)
	}
	sch, err := c.Compile(CatalogID)
	if err != nil {
		return nil, fmt.Errorf("compiling embedded catalog schema: %w", err)
	}
	return sch, nil
})

// ValidateCatalog checks a catalog document that has already been turned into JSON.
//
// The file people write is YAML, for the same reason the conventions file is:
// it is written by hand and the reason for each entry belongs next to it. The
// schema stays JSON so there is one description of the shape rather than two.
func ValidateCatalog(doc []byte) error {
	sch, err := compileCatalog()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return fmt.Errorf("parsing document: %w", err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("document does not match the catalog schema: %w", err)
	}
	return nil
}
