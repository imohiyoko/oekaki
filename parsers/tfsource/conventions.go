package tfsource

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/imohiyoko/oekaki/schema"
	"gopkg.in/yaml.v3"
)

// Conventions says where to look in a repository for facts Terraform does not
// standardise.
//
// An account id is the one that matters. Terraform has no idea which account a
// module deploys into: the provider knows, and how the provider is told
// differs per estate. assume_role with a literal ARN needs nothing here and is
// read without it, but an estate that keeps the id in a local, or in a table
// the module picks a row from, has to say so.
//
// The values are names, not data — which local, which variable. That keeps the
// file small enough to read and means it says nothing about what is deployed.
type Conventions struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Note    string `json:"note,omitempty"`

	// AccountFromLocal are locals that hold an account id outright, most
	// trusted first.
	AccountFromLocal []string `json:"accountFromLocal,omitempty"`

	// AccountTable is a name-to-id table and the name a module uses to pick
	// its row.
	AccountTable *AccountTable `json:"accountTable,omitempty"`

	rules []rule
}

type AccountTable struct {
	Variable string `json:"variable"`
}

type rule struct {
	direct *regexp.Regexp // a local holding the id
	table  *regexp.Regexp // the table block
	pick   *regexp.Regexp // which row this module takes
}

// ReadConventions loads a conventions file.
//
// The file is YAML because a person writes it and the reason for each entry
// belongs beside it; JSON cannot carry a comment. It is converted to JSON and
// checked against the same schema every other document here uses, so there is
// one description of the shape rather than one per format.
func ReadConventions(path string) (*Conventions, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var loose any
	if err := yaml.Unmarshal(raw, &loose); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	asJSON, err := json.Marshal(loose)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := schema.ValidateConventions(asJSON); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var c Conventions
	if err := json.Unmarshal(asJSON, &c); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	c.compile()
	return &c, nil
}

func (c *Conventions) compile() {
	if c == nil {
		return
	}
	for _, name := range c.AccountFromLocal {
		q := regexp.QuoteMeta(name)
		c.rules = append(c.rules, rule{
			direct: regexp.MustCompile(`(?m)(?:^|[\s{])` + q + `\s*=\s*"?(\d{12})"?`),
		})
	}
	if c.AccountTable != nil && c.AccountTable.Variable != "" {
		q := regexp.QuoteMeta(c.AccountTable.Variable)
		c.rules = append(c.rules, rule{
			table: regexp.MustCompile(`(?m)(?:^|[\s{])` + q + `\s*=\s*\{`),
			pick:  regexp.MustCompile(q + `(?:\.([A-Za-z0-9_-]+)|\[\s*"([A-Za-z0-9_-]+)"\s*\])`),
		})
	}
}

var tablePair = regexp.MustCompile(`(?m)^\s*"?([A-Za-z0-9_-]+)"?\s*=\s*"?(\d{12})"?`)

// account applies the conventions to one module's source, in the order they
// were written.
//
// A table with more than one row and nothing saying which row this module
// takes returns nothing. Picking one would be right sometimes, and the times
// it is wrong draw the module inside another account's estate — which is not a
// gap somebody notices.
func (c *Conventions) account(text string) string {
	if c == nil {
		return ""
	}
	for _, r := range c.rules {
		if r.direct != nil {
			if m := r.direct.FindStringSubmatch(text); m != nil {
				return m[1]
			}
			continue
		}
		loc := r.table.FindStringIndex(text)
		if loc == nil {
			continue
		}
		body, _ := block(text, loc[1]-1)
		rows := map[string]string{}
		for _, m := range tablePair.FindAllStringSubmatch(body, -1) {
			rows[m[1]] = m[2]
		}
		if len(rows) == 0 {
			continue
		}
		if m := r.pick.FindStringSubmatch(text); m != nil {
			key := m[1]
			if key == "" {
				key = m[2]
			}
			if id, ok := rows[key]; ok {
				return id
			}
		}
		if len(rows) == 1 {
			for _, id := range rows {
				return id
			}
		}
	}
	return ""
}
