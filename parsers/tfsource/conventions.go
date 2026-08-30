package tfsource

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

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
			direct: regexp.MustCompile(`(?m)(?:^|[\s{])` + q + `\s*=\s*"?(\d{12,})"?`),
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

var tablePair = regexp.MustCompile(`(?m)^\s*"?([A-Za-z0-9_-]+)"?\s*=\s*"?(\d{12,})"?`)

// An account id is twelve digits exactly. A longer run of digits is a
// different value, not an account with something on the end, and its first
// twelve name an estate somebody else owns.
func isAccount(digits string) bool { return len(digits) == 12 }

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
			for _, m := range r.direct.FindAllStringSubmatch(text, -1) {
				if isAccount(m[1]) {
					return m[1]
				}
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
			if isAccount(m[2]) {
				rows[m[1]] = m[2]
			}
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

// ReadConventionsDir reads every conventions file in a directory and folds
// them into one.
//
// Files are read in filename order and the later one wins where they disagree,
// which is how a personal file sitting beside the shared one adds a local
// naming habit without restating the rest. A directory that is not there
// yields no conventions rather than an error: having none is the ordinary
// case, and the parser reads an estate that needs none without any of this.
func ReadConventionsDir(dir string) (*Conventions, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)

	out := &Conventions{Kind: "oekaki.conventions"}
	for _, name := range names {
		one, err := ReadConventions(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out.Version = one.Version
		// Locals accumulate, most trusted first, because listing another place
		// an id might be is adding to the search rather than replacing it. The
		// table is one table, so naming a second one replaces the first.
		out.AccountFromLocal = append(out.AccountFromLocal, one.AccountFromLocal...)
		if one.AccountTable != nil {
			out.AccountTable = one.AccountTable
		}
	}
	out.rules = nil
	out.compile()
	return out, nil
}
