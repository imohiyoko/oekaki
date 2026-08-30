// Package catalog holds what the generated files are called, in the language
// of whoever generated them.
//
// This program knows a directory holds files. It does not know that one of
// them is an estate overview, that another is a table of accounts, or that a
// reader would group the two differently. Those are facts about one
// deployment, in one organisation's vocabulary, and if they were written into
// the program then every deployment would be shown somebody else's words —
// and this program's source would be carrying somebody else's words too.
//
// So the whole listing is described from outside: what the groups are, what
// each file is called, what it is for, what order to show them in. Nothing in
// this package is in any particular language.
package catalog

import (
	"maps"
	"path"
	"sort"
)

// Kind is a group items fall into.
type Kind struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Rule describes the files whose name matches a pattern.
type Rule struct {
	Match  string `json:"match"`
	Kind   string `json:"kind,omitempty"`
	Title  string `json:"title,omitempty"`
	About  string `json:"about,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

// Catalog is one deployment's description of its own output.
type Catalog struct {
	Kind    string            `json:"kind"`
	Version string            `json:"version"`
	Note    string            `json:"note,omitempty"`
	Title   string            `json:"title,omitempty"`
	Kinds   []Kind            `json:"kinds,omitempty"`
	Items   []Rule            `json:"items,omitempty"`
	Theme   map[string]string `json:"theme,omitempty"`
}

// Entry is what to show for one file.
type Entry struct {
	Name  string
	Kind  string
	Label string // the kind's label, or the kind's id when it has no label
	Title string
	About string

	// Rank is the position of the rule that matched, so that the order rules
	// are written in is the order things appear on screen. A file no rule
	// matched sorts after every file one did.
	Rank int
}

// unmatched is the rank of a file no rule described. It has to sort after
// everything a rule named without any arithmetic on a slice length that could
// overflow into the matched range.
const unmatched = int(^uint(0) >> 1)

// Describe says what to show for one file.
//
// The first rule whose pattern matches wins. Rules are read top to bottom the
// way they are written, so the more specific one goes above the catch-all —
// the opposite of a CODEOWNERS file, and worth knowing if you have just come
// from one.
//
// A file nothing matched is still described, with its own name. Leaving it out
// would mean adding a file to the pipeline makes it invisible until somebody
// remembers to describe it, and the failure would be silence.
func (c *Catalog) Describe(name string) Entry {
	out := Entry{Name: name, Title: name, Rank: unmatched}
	if c == nil {
		return out
	}
	for i, r := range c.Items {
		ok, err := path.Match(r.Match, name)
		if err != nil || !ok {
			continue
		}
		out.Rank = i
		out.Kind = r.Kind
		if r.Title != "" {
			out.Title = r.Title
		}
		out.About = r.About
		if r.Hidden {
			return Entry{}
		}
		break
	}
	out.Label = c.labelOf(out.Kind)
	return out
}

// Hidden reports whether a file should be left out of the listing.
func (c *Catalog) Hidden(name string) bool {
	if c == nil {
		return false
	}
	for _, r := range c.Items {
		if ok, err := path.Match(r.Match, name); err == nil && ok {
			return r.Hidden
		}
	}
	return false
}

func (c *Catalog) labelOf(kind string) string {
	for _, k := range c.Kinds {
		if k.ID == kind {
			return k.Label
		}
	}
	return kind
}

// List describes every name, drops the hidden ones, and puts them in the order
// the catalog asked for.
//
// Files no rule matched keep their own names and go last, sorted among
// themselves so that the listing does not change order between runs for
// reasons nobody chose.
func (c *Catalog) List(names []string) []Entry {
	out := make([]Entry, 0, len(names))
	for _, n := range names {
		e := c.Describe(n)
		if e.Name == "" {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Merge folds another catalog into this one.
//
// Rules and kinds accumulate in the order the files were read, because their
// order is meaningful — it is the order things are shown in, and the order
// matching is tried in. A title or a theme is a single value, so a later file
// replaces it; that is how a personal file sitting after the shared one
// changes the look without restating everything.
func (c *Catalog) Merge(other *Catalog) {
	if other == nil {
		return
	}
	if other.Title != "" {
		c.Title = other.Title
	}
	c.Kinds = append(c.Kinds, other.Kinds...)
	c.Items = append(c.Items, other.Items...)
	if len(other.Theme) > 0 && c.Theme == nil {
		c.Theme = map[string]string{}
	}
	maps.Copy(c.Theme, other.Theme)
}
