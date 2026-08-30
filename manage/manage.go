// Package manage keeps what people did to the drawings, apart from the
// drawings themselves.
//
// A generation of output is disposable: re-run the pipeline and it is written
// again from scratch. The things in here are not. Which saved layout should be
// used from now on, what somebody wrote down about an item, who did what — all
// of it outlives the run it was first attached to, and none of it can be
// recomputed. So it lives in one directory of its own rather than inside any
// generation, and deleting a generation must not be able to take it.
//
// The store owns a directory. Layout and overlay documents already have a home
// under it (see internal/serve); this package adds the parts that say which of
// them is the current one, what humans annotated, and what changed.
//
// # Why writes here are atomic and the journal is not
//
// The defaults file and an item's annotation are read whole and replaced
// whole, so a crash halfway through a write would leave a truncated JSON
// document where a valid one used to be — the previous good state destroyed by
// the act of recording a new one. Both are written to a temporary file in the
// same directory and renamed over the target.
//
// The journal is appended to, never rewritten, so the failure it has to
// survive is a torn last line rather than a lost file. Readers skip lines they
// cannot parse.
package manage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// ErrRefused is what a caller did wrong, as opposed to what went wrong.
//
// The two used to be one thing, and the HTTP layer could only tell them apart
// by exception type; three packages each defining their own meant a mistyped
// name came back as a server error. One sentinel, one meaning: this is the
// caller's, show the message to them.
var ErrRefused = errors.New("refused")

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// safeName is what may appear as a path segment. Anything else would let a
// name reach out of the directory it belongs in.
var safeName = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._-]{0,63}\z`)

// CheckName refuses a name that could leave its folder.
func CheckName(name string) error {
	if !safeName.MatchString(name) {
		return refuse("%q is not a name that can be used here", name)
	}
	return nil
}

// Store is a directory holding everything that outlives a generation.
//
// It is a value rather than a set of functions taking a root, because there
// are four separate files under it and passing the same string to all of them
// invites one call site to point at a different directory than the rest.
type Store struct {
	root string

	// Every file under here is read whole, changed, and written whole. The
	// replace is atomic; the gap between the read and it is not, and net/http
	// runs a goroutine per request, so two people promoting different pages at
	// the same time is an ordinary Tuesday rather than a race worth ignoring.
	// The later write would otherwise carry a map that never saw the earlier
	// one, and one of the two changes would vanish with nothing reporting it.
	//
	// This serializes one process. Several processes sharing a state directory
	// would need a lock in the filesystem, which is not built and is not the
	// shape anybody runs this in yet.
	mu sync.Mutex
}

// At opens the store rooted at dir. The directory is created as needed by the
// first write; opening does not create it, so that a read-only caller pointed
// at the wrong path finds nothing instead of leaving an empty tree behind.
func At(dir string) *Store { return &Store{root: dir} }

// Root is the directory this store owns.
func (s *Store) Root() string { return s.root }

const (
	defaultsFile = "defaults.json"
	journalFile  = "journal.jsonl"
	metaDir      = "meta"
)

// Actor is who did something and how well that is known.
//
// The two travel together everywhere. Today every name is self-asserted — a
// header the caller filled in — and one day some of them will have been
// checked by an identity provider. Recording only the name would make those
// two indistinguishable in hindsight, and the whole value of an audit trail is
// being able to say which it was.
type Actor struct {
	Name   string
	Origin string
}

// Unverified is the origin of a name nobody checked.
const Unverified = "self-asserted"

// Anonymous is what an entry says when no name was given at all. Recording a
// blank would read as a missing field rather than as what actually happened.
const Anonymous = "(no name given)"

func (a Actor) who() string {
	if a.Name == "" {
		return Anonymous
	}
	return a.Name
}

func (a Actor) origin() string {
	if a.Origin == "" {
		return Unverified
	}
	return a.Origin
}

// now is the clock, replaceable in tests so that a recorded time can be
// asserted on.
var now = time.Now

func stamp() string { return now().Format(time.RFC3339) }

// writeAtomic replaces path with body, or leaves what was there.
func writeAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manage-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// readJSON reads a document, treating a missing file as an empty one.
//
// A store that has never been written to is not an error state; it is where
// everyone starts. Callers would all have to special-case os.IsNotExist
// otherwise, and one of them would forget.
func readJSON(path string, into any) error {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, into)
}

func marshal(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
