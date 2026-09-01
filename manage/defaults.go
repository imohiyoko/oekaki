package manage

import (
	"os"
	"path/filepath"

	"github.com/imohiyoko/oekaki/internal/serve"
)

// Default is which saved layout a page is drawn with from now on, and who
// decided that.
//
// Promoting one is the moment a private edit becomes everybody's, so the
// decision is recorded next to the choice rather than only in the journal —
// the question "why does this look like this" is asked of the page, not of the
// audit trail.
type Default struct {
	Version string `json:"version"`
	By      string `json:"by"`
	Origin  string `json:"origin"`
	At      string `json:"at"`
}

func (s *Store) defaultsPath() string { return filepath.Join(s.root, defaultsFile) }

// Defaults is every page that has one.
func (s *Store) Defaults() (map[string]Default, error) {
	out := map[string]Default{}
	if err := readJSON(s.defaultsPath(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DefaultFor is the default for one page, if it has one.
func (s *Store) DefaultFor(page string) (Default, bool, error) {
	all, err := s.Defaults()
	if err != nil {
		return Default{}, false, err
	}
	d, ok := all[page]
	return d, ok, nil
}

// Promote makes a saved version the one this page is drawn with.
//
// A version that is not there is refused rather than recorded, because a
// default pointing at nothing draws the page without any human layout at all
// and looks, from the outside, exactly like nobody having promoted anything.
func (s *Store) Promote(page, name string, who Actor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.promote(page, name, who)
}

func (s *Store) promote(page, name string, who Actor) error {
	path, err := serve.Path(s.root, page, name)
	if err != nil {
		return refuse("%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		return refuse("there is no version %q saved for %q", name, page)
	}

	all, err := s.Defaults()
	if err != nil {
		return err
	}
	all[page] = Default{Version: name, By: who.who(), Origin: who.origin(), At: stamp()}
	body, err := marshal(all)
	if err != nil {
		return err
	}
	if err := writeAtomic(s.defaultsPath(), body); err != nil {
		return err
	}
	_, err = s.Record(who, ActionPromote, page, map[string]any{"version": name})
	return err
}

// Demote takes a page back to being drawn the way it is generated.
//
// It reports whether there was anything to take back. A page that had no
// default is left alone and nothing is written to the journal: recording that
// somebody undid a thing nobody had done would be an entry about no change,
// and the journal is only worth reading if everything in it is a change.
func (s *Store) Demote(page string, who Actor) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.demote(page, who)
}

// demote is Demote with the lock already held. Forget needs it, and taking the
// lock twice in one goroutine would stop there and never come back.
func (s *Store) demote(page string, who Actor) (bool, error) {
	all, err := s.Defaults()
	if err != nil {
		return false, err
	}
	if _, ok := all[page]; !ok {
		return false, nil
	}
	delete(all, page)
	body, err := marshal(all)
	if err != nil {
		return false, err
	}
	if err := writeAtomic(s.defaultsPath(), body); err != nil {
		return false, err
	}
	if _, err := s.Record(who, ActionDemote, page, nil); err != nil {
		return true, err
	}
	return true, nil
}

// Forget deletes a saved version.
//
// If it was the default, the page is demoted in the same breath. Leaving the
// pointer behind would mean the next drawing quietly comes out without the
// layout somebody had chosen, with nothing anywhere saying why.
func (s *Store) Forget(page, name string, who Actor) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := serve.Path(s.root, page, name)
	if err != nil {
		return refuse("%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		return refuse("there is no version %q saved for %q", name, page)
	}
	if err := serve.Remove(s.root, page, name); err != nil {
		return err
	}
	// Recording comes first but must not be able to stop what follows. The
	// file is already gone; leaving the pointer to it behind would draw the
	// page plain from now on with nothing saying why, and a journal that could
	// not be written is not a reason to leave the store inconsistent.
	_, journal := s.Record(who, ActionForget, page, map[string]any{"version": name})

	current, ok, err := s.DefaultFor(page)
	if err != nil {
		return err
	}
	if ok && current.Version == name {
		if _, err := s.demote(page, who); err != nil {
			return err
		}
	}
	return journal
}

// LayoutFor is the file a page should be drawn with, or empty if none.
//
// A default naming a version that is no longer there returns empty rather than
// an error. Drawing the page without the human layout is a worse picture;
// refusing to draw it is no picture, and no picture is worse than a worse
// picture. StaleDefault is how the same situation gets said out loud.
func (s *Store) LayoutFor(page string) (string, error) {
	d, ok, err := s.DefaultFor(page)
	if err != nil || !ok {
		return "", err
	}
	if !s.Honours(page, d.Version) {
		return "", nil
	}
	path, err := serve.Path(s.root, page, d.Version)
	if err != nil {
		return "", nil
	}
	return path, nil
}

// Honours reports whether the version a default names is still on disk.
//
// It is the one question that separates a decision being followed from one
// pointing at nothing, and it is asked from three places — what to draw with,
// what to say is stale, and what to show in a listing. Having it written once
// is what stops those three from drifting into disagreeing about the same
// page.
//
// It takes the version rather than reading the default itself, so a caller
// that already holds the whole map can ask about every page without reading
// that file again per page.
func (s *Store) Honours(page, version string) bool {
	path, err := serve.Path(s.root, page, version)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// StaleDefault is the name of a promoted version whose file has gone, or
// empty.
//
// This is the loud half of the pair with LayoutFor. Something has to say that
// a choice somebody made is no longer being honoured, or the page simply comes
// out plain and everyone assumes that is what was asked for.
func (s *Store) StaleDefault(page string) (string, error) {
	d, ok, err := s.DefaultFor(page)
	if err != nil || !ok {
		return "", err
	}
	if !s.Honours(page, d.Version) {
		return d.Version, nil
	}
	return "", nil
}
