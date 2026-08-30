package manage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Claim records who said this and how well that is known.
//
// Every value here is currently self-asserted — somebody typed their own name
// into a box. Writing the origin down anyway is what makes it possible, later,
// to tell a name an identity provider vouched for from a name somebody chose.
// Adding the field afterwards would leave every existing annotation
// indistinguishable from a checked one.
type Claim struct {
	SetBy  string `json:"set_by"`
	Origin string `json:"origin"`
	SetAt  string `json:"set_at"`
}

// Meta is what a person wrote down about an item, as opposed to what was
// generated about it.
//
// It is kept out of any generation because it answers questions the generated
// output cannot — who looks after this, why it exists, who may see it — and
// those answers stay true after the drawing is redrawn.
type Meta struct {
	Title       string   `json:"title,omitempty"`
	CreatedBy   string   `json:"created_by,omitempty"`
	Maintainers []string `json:"maintainers,omitempty"`
	Note        string   `json:"note,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	// ReadRoles limits who may see the item. Empty is no limit.
	ReadRoles []string `json:"read_roles,omitempty"`

	Claim *Claim `json:"claim,omitempty"`
}

// blank reports whether a person wrote nothing at all.
//
// Claim is not consulted: it is this package's own stamp, and counting it
// would mean an annotation someone emptied out stays on disk forever, kept
// alive by the record of who emptied it.
func (m Meta) blank() bool {
	return m.Title == "" && m.CreatedBy == "" && m.Note == "" &&
		len(m.Maintainers) == 0 && len(m.Tags) == 0 && len(m.ReadRoles) == 0
}

func (s *Store) metaPath(item string) (string, error) {
	if err := CheckName(item); err != nil {
		return "", err
	}
	return filepath.Join(s.root, metaDir, item+".json"), nil
}

// Meta is what was written down about one item. An item nobody annotated
// comes back blank rather than missing, because "nothing written" is the
// normal case and every caller would otherwise repeat the same check.
func (s *Store) Meta(item string) (Meta, error) {
	path, err := s.metaPath(item)
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := readJSON(path, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// AllMeta is everything written down, keyed by item.
func (s *Store) AllMeta() (map[string]Meta, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, metaDir))
	if os.IsNotExist(err) {
		return map[string]Meta{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]Meta, len(entries))
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		if e.IsDir() || name == e.Name() {
			continue
		}
		m, err := s.Meta(name)
		if err != nil {
			continue
		}
		out[name] = m
	}
	return out, nil
}

// Annotate replaces what was written about an item.
//
// known is the roles that exist. A ReadRoles naming something else is refused
// here rather than at reading time, because the failure at reading time is
// silence — the limit matches nobody, the item is hidden from everybody, and
// the person who typed the name sees a working save.
//
// Emptying every field deletes the annotation rather than leaving a document
// containing only this package's own stamp.
func (s *Store) Annotate(item string, in Meta, who Actor, known []string) (Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.metaPath(item)
	if err != nil {
		return Meta{}, err
	}

	have := make(map[string]bool, len(known))
	for _, r := range known {
		have[r] = true
	}
	var unknown []string
	for _, r := range in.ReadRoles {
		if !have[r] {
			unknown = append(unknown, r)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Meta{}, refuse("no such role: %s", strings.Join(unknown, ", "))
	}

	if in.blank() {
		if err := s.erase(item, who); err != nil {
			return Meta{}, err
		}
		return Meta{}, nil
	}

	in.Claim = &Claim{SetBy: who.who(), Origin: who.origin(), SetAt: stamp()}
	body, err := marshal(in)
	if err != nil {
		return Meta{}, err
	}
	if err := writeAtomic(path, body); err != nil {
		return Meta{}, err
	}
	if _, err := s.Record(who, ActionAnnotate, item, nil); err != nil {
		return in, err
	}
	return in, nil
}

// Erase removes what was written about an item. Erasing nothing is not an
// error and is not recorded.
func (s *Store) Erase(item string, who Actor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.erase(item, who)
}

func (s *Store) erase(item string, who Actor) error {
	path, err := s.metaPath(item)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	_, err = s.Record(who, ActionErase, item, nil)
	return err
}
