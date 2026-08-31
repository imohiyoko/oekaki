package manage

import (
	"path/filepath"
	"sort"
)

const screensFile = "screens.json"

// What one person may keep.
//
// A screening is written by whoever is looking, which on a server that
// authorizes nobody means anybody, so something has to stop the file from
// growing without end. The numbers are generous enough that nobody working
// normally will meet them and small enough that meeting them is not a way to
// fill a disk.
const (
	screenQueryMax    = 2048
	screensPerSubject = 64
)

// Screen is a set of conditions somebody wants back tomorrow.
//
// Query is kept as it arrived and is not interpreted here. This package knows
// that a person narrowed a listing; it does not know what the listing is or
// what its conditions mean, and it must not learn — the vocabulary belongs to
// the page, which changes on its own schedule.
//
// The cost of that is a rule the reader has to keep: whatever comes back out
// of here is a string somebody typed, so the page parses it into the
// conditions it recognises and renders those, rather than putting it into a
// link as it stands. A screening saved by an older build, or by hand, is
// therefore narrowed to what the current page understands instead of being
// carried through unread.
type Screen struct {
	Name  string `json:"name"`
	Query string `json:"query"`
	Claim *Claim `json:"claim,omitempty"`
}

func (s *Store) screensPath() string { return filepath.Join(s.root, screensFile) }

// AllScreens is every screening anybody kept, by subject.
func (s *Store) AllScreens() (map[string][]Screen, error) {
	out := map[string][]Screen{}
	if err := readJSON(s.screensPath(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Screens is what one person kept, by name.
//
// A person who kept nothing gets an empty list rather than a missing one:
// having no screenings is where everybody starts, and it is not a condition
// any caller should have to tell apart from a failure.
func (s *Store) Screens(who Actor) ([]Screen, error) {
	all, err := s.AllScreens()
	if err != nil {
		return nil, err
	}
	return all[who.who()], nil
}

// SaveScreen keeps a set of conditions under a name, replacing one of the same
// name.
//
// It is filed under whoever is asking, and there is no way to file it under
// anybody else — a screening is a private convenience, and one person tidying
// away another's view of a listing is not a thing this should be able to
// express.
//
// Nothing goes in the journal. That file is for changes somebody other than
// the person making them can see, which is the same reason saving a layout is
// not recorded and promoting one is.
func (s *Store) SaveScreen(who Actor, name, query string) (Screen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := CheckName(name); err != nil {
		return Screen{}, err
	}
	if len(query) > screenQueryMax {
		return Screen{}, refuse("that screening is %d characters long and %d is the most that can be kept",
			len(query), screenQueryMax)
	}

	all, err := s.AllScreens()
	if err != nil {
		return Screen{}, err
	}
	subject := who.who()
	kept := all[subject]

	out := Screen{Name: name, Query: query,
		Claim: &Claim{SetBy: subject, Origin: who.origin(), SetAt: stamp()}}

	replaced := false
	for i, existing := range kept {
		if existing.Name == name {
			kept[i], replaced = out, true
			break
		}
	}
	if !replaced {
		if len(kept) >= screensPerSubject {
			return Screen{}, refuse("%s already keeps %d screenings, which is the most; forget one first",
				subject, screensPerSubject)
		}
		kept = append(kept, out)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Name < kept[j].Name })
	all[subject] = kept

	body, err := marshal(all)
	if err != nil {
		return Screen{}, err
	}
	if err := writeAtomic(s.screensPath(), body); err != nil {
		return Screen{}, err
	}
	return out, nil
}

// ForgetScreen drops one of somebody's screenings, and reports whether there
// was one to drop.
func (s *Store) ForgetScreen(who Actor, name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.AllScreens()
	if err != nil {
		return false, err
	}
	subject := who.who()
	kept := all[subject]
	out := kept[:0:0]
	for _, existing := range kept {
		if existing.Name != name {
			out = append(out, existing)
		}
	}
	if len(out) == len(kept) {
		return false, nil
	}
	if len(out) == 0 {
		// An empty list would leave the subject in the file forever, which
		// turns "who has ever narrowed a listing" into something the file
		// answers by accident.
		delete(all, subject)
	} else {
		all[subject] = out
	}
	body, err := marshal(all)
	if err != nil {
		return false, err
	}
	if err := writeAtomic(s.screensPath(), body); err != nil {
		return false, err
	}
	return true, nil
}
