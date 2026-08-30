package manage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// Entry is one thing somebody did.
type Entry struct {
	At     string         `json:"at"`
	Actor  string         `json:"actor"`
	Origin string         `json:"origin"`
	Action string         `json:"action"`
	Target string         `json:"target"`
	Detail map[string]any `json:"detail,omitempty"`
}

// The actions worth recording. Each one changes what somebody else sees.
const (
	ActionPromote  = "promote"
	ActionDemote   = "demote"
	ActionForget   = "forget"
	ActionAnnotate = "annotate"
	ActionErase    = "erase"
)

// Record appends one entry.
//
// Only changes visible to someone other than the person making them belong
// here. Saving a private version of a layout affects nobody and would bury the
// entries that matter under entries that do not; making that version the
// default changes what everyone sees from then on, and belongs.
//
// Failing to record is not failing to do the thing. The caller has already
// acted by the time this is called, so a journal that cannot be written is
// reported and the action stands — the alternative is refusing work because
// the log is full, which is worse.
func (s *Store) Record(who Actor, action, target string, detail map[string]any) (Entry, error) {
	e := Entry{
		At:     stamp(),
		Actor:  who.who(),
		Origin: who.origin(),
		Action: action,
		Target: target,
		Detail: detail,
	}
	body, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	path := filepath.Join(s.root, journalFile)
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return e, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return e, err
	}
	defer f.Close()
	if _, err := f.Write(append(body, '\n')); err != nil {
		return e, err
	}
	return e, nil
}

// History is what was done, newest first.
//
// target narrows to one subject; empty means everything. limit caps the
// result; zero or less means all of it.
//
// A line that will not parse is skipped rather than ending the read. The file
// is appended to and a crash can tear the last line, and one torn line must
// not hide every entry written before it.
func (s *Store) History(target string, limit int) ([]Entry, error) {
	f, err := os.Open(filepath.Join(s.root, journalFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var all []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if target != "" && e.Target != target {
			continue
		}
		all = append(all, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// Newest first: the question being asked is almost always "what just
	// happened", not "what happened first".
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
