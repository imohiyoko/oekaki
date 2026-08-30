// Package config reads the directory that tells this program the things it
// cannot work out for itself.
//
// There are three of them, and they are kept in three directories rather than
// three sections of one file, because they are edited by different people at
// different times for different reasons: how to read a repository, who may see
// what, and what the output is called.
//
//	<config>/conventions/   where to find facts Terraform does not standardise
//	<config>/roles/         what roles exist and what each may do
//	<config>/catalog/       what the generated files are called
//
// Every *.yaml and *.yml in a directory is read, in filename order, and folded
// together. A directory rather than a file so that a shared description and a
// personal one can sit side by side: name the personal one so it sorts last
// and it wins, without anybody having to restate the parts they agree with.
//
// # What is deliberately not here
//
// Who holds which role is not configuration. It changes while the program
// runs, by somebody clicking, and it lives with the rest of the state.
//
// Neither are credentials. This program reads them from the environment when
// it needs them and never writes them anywhere, which is the same promise the
// rest of it makes about the cloud.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/imohiyoko/oekaki/authz"
	"github.com/imohiyoko/oekaki/catalog"
	"github.com/imohiyoko/oekaki/parsers/tfsource"
	"github.com/imohiyoko/oekaki/schema"
	"gopkg.in/yaml.v3"
)

// The environment variables, and what they are for.
const (
	// EnvConfig is where the three directories above live.
	EnvConfig = "OEKAKI_CONFIG"

	// EnvState is where everything that outlives a generation is kept.
	EnvState = "OEKAKI_STATE"
)

// DefaultDir is the config directory when nobody said otherwise. A dotted name
// because it belongs to the working tree it sits in and is not part of what
// gets published from it.
const DefaultDir = ".oekaki"

// The names of the three directories inside it.
const (
	ConventionsDir = "conventions"
	RolesDir       = "roles"
	CatalogDir     = "catalog"
)

// Dir resolves where the configuration is: what was asked for on the command
// line, else the environment, else the default.
//
// A flag beats the environment because it is in front of the person running
// the command, and the environment is not.
func Dir(flag string) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv(EnvConfig); v != "" {
		return v
	}
	return DefaultDir
}

// StateDir resolves where state is kept, falling back to a directory beside
// whatever is being served.
func StateDir(flag, beside string) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv(EnvState); v != "" {
		return v
	}
	return filepath.Join(beside, ".oekaki-state")
}

// Config is everything the directory said.
type Config struct {
	Dir         string
	Roles       authz.Policy
	Catalog     *catalog.Catalog
	Conventions *tfsource.Conventions
}

// Load reads a configuration directory.
//
// A directory that is not there is not an error: everything in it is optional,
// and a deployment that has not written any of it yet should still start.
// Missing configuration and broken configuration are different, and only the
// second one should stop anything.
func Load(dir string) (*Config, error) {
	out := &Config{Dir: dir, Catalog: &catalog.Catalog{}}

	roles, err := loadRoles(filepath.Join(dir, RolesDir))
	if err != nil {
		return nil, err
	}
	out.Roles = roles

	cat, err := loadCatalog(filepath.Join(dir, CatalogDir))
	if err != nil {
		return nil, err
	}
	out.Catalog = cat

	conv, err := tfsource.ReadConventionsDir(filepath.Join(dir, ConventionsDir))
	if err != nil {
		return nil, err
	}
	out.Conventions = conv

	return out, nil
}

// files lists the YAML in a directory, in filename order.
//
// The order is the whole point of allowing more than one: a file named so it
// sorts last is how somebody overrides the shared description without editing
// it. Sorting has to be by name and not by whatever the filesystem returns.
func files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".yaml", ".yml":
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// asJSON turns one hand-written YAML file into the JSON the schemas describe.
func asJSON(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var loose any
	if err := yaml.Unmarshal(raw, &loose); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	b, err := json.Marshal(loose)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return b, nil
}

// rolesDoc is one roles file. It deliberately has no place to say who holds a
// role: that is state, and a file that could carry it would let one be shipped.
type rolesDoc struct {
	Kind    string                  `json:"kind"`
	Version string                  `json:"version"`
	Note    string                  `json:"note,omitempty"`
	Roles   map[string][]authz.Rule `json:"roles"`

	// Pointers, so that a later file can take something back. A plain slice
	// and a plain bool cannot say the difference between "I did not mention
	// this" and "I am turning this off", and the second one is how a personal
	// file revokes what a shared file granted. Reading them the same way means
	// the only direction configuration can move is looser.
	Anonymous            *[]string `json:"anonymous,omitempty"`
	AllowWithoutRepoRead *bool     `json:"allowWithoutRepoRead,omitempty"`
}

func loadRoles(dir string) (authz.Policy, error) {
	out := authz.Policy{Roles: map[string][]authz.Rule{}}
	paths, err := files(dir)
	if err != nil {
		return out, err
	}
	for _, p := range paths {
		body, err := asJSON(p)
		if err != nil {
			return out, err
		}
		if err := schema.ValidateRoles(body); err != nil {
			return out, fmt.Errorf("%s: %w", p, err)
		}
		var doc rolesDoc
		if err := json.Unmarshal(body, &doc); err != nil {
			return out, fmt.Errorf("reading %s: %w", p, err)
		}
		// A role is replaced whole by a later file rather than having its
		// rules appended to. Half of one file's idea of "viewer" and half of
		// another's is not something anybody meant to write.
		for name, rules := range doc.Roles {
			out.Roles[name] = rules
		}
		if doc.Anonymous != nil {
			out.Anonymous = *doc.Anonymous
		}
		if doc.AllowWithoutRepoRead != nil {
			out.AllowWithoutRepoRead = *doc.AllowWithoutRepoRead
		}
	}

	// Checking here means a typo is found when the program starts rather than
	// when somebody is refused something and cannot see why.
	if err := authz.Check(out); err != nil {
		return out, fmt.Errorf("%s: %w", dir, err)
	}
	return out, nil
}

func loadCatalog(dir string) (*catalog.Catalog, error) {
	out := &catalog.Catalog{}
	paths, err := files(dir)
	if err != nil {
		return out, err
	}
	for _, p := range paths {
		body, err := asJSON(p)
		if err != nil {
			return out, err
		}
		if err := schema.ValidateCatalog(body); err != nil {
			return out, fmt.Errorf("%s: %w", p, err)
		}
		var doc catalog.Catalog
		if err := json.Unmarshal(body, &doc); err != nil {
			return out, fmt.Errorf("reading %s: %w", p, err)
		}
		out.Merge(&doc)
	}
	return out, nil
}

// ErrNoConfig is returned by Require when the directory is not there.
var ErrNoConfig = errors.New("no configuration directory")

// Require is Load, but refuses a directory that does not exist.
//
// Most callers want Load: an absent directory means defaults. A caller that
// was pointed at one explicitly wants to hear that the path is wrong rather
// than silently running with nothing.
func Require(dir string) (*Config, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNoConfig, dir)
	}
	return Load(dir)
}
