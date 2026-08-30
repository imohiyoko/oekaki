package manage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imohiyoko/oekaki/internal/serve"
)

// A layout document the store's validator accepts. The nodes do not have to
// correspond to anything: this package decides which version is current, not
// whether the coordinates land.
const layoutDoc = `{"kind":"oekaki.layout","version":"0.2",` +
	`"nodes":[{"id":"a","x":10,"y":20}],"claim":{"origin":"human"}}`

func store(t *testing.T) *Store {
	t.Helper()
	return At(t.TempDir())
}

// save puts a version where Promote can find it.
func save(t *testing.T, s *Store, page, name string) {
	t.Helper()
	if err := serve.Save(s.Root(), page, name, []byte(layoutDoc)); err != nil {
		t.Fatal(err)
	}
}

func actor() Actor { return Actor{Name: "github:someone", Origin: Unverified} }

// fixedClock makes a recorded time assertable.
func fixedClock(t *testing.T) {
	t.Helper()
	was := now
	now = func() time.Time { return time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = was })
}

// A store nobody has written to is where everyone starts, so reading one has
// to be ordinary rather than an error every caller remembers to special-case.
func TestAnUntouchedStoreReadsAsEmpty(t *testing.T) {
	s := store(t)
	if got, err := s.Defaults(); err != nil || len(got) != 0 {
		t.Fatalf("defaults: %#v %v", got, err)
	}
	if got, err := s.AllMeta(); err != nil || len(got) != 0 {
		t.Fatalf("meta: %#v %v", got, err)
	}
	if got, err := s.History("", 0); err != nil || len(got) != 0 {
		t.Fatalf("history: %#v %v", got, err)
	}
	if got, err := s.LayoutFor("core"); err != nil || got != "" {
		t.Fatalf("layout: %q %v", got, err)
	}
}

// Opening a store must not create anything. A caller pointed at the wrong path
// should find nothing there, not leave an empty tree behind for the next
// person to wonder about.
func TestOpeningAStoreWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet")
	s := At(dir)
	if _, err := s.Defaults(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the directory was created by reading: %v", err)
	}
}

// Promoting is the moment a private edit becomes everyone's, so the page has
// to come back drawn with it.
func TestAPromotedVersionIsWhatThePageIsDrawnWith(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	got, err := s.LayoutFor("core")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join("core", "wide.layout.json")) {
		t.Fatalf("the wrong file came back: %q", got)
	}
}

// A default pointing at nothing draws the page plain, which from the outside
// is indistinguishable from nobody having promoted anything. Refuse it at the
// point where somebody can still fix it.
func TestAVersionThatIsNotThereCannotBecomeTheDefault(t *testing.T) {
	s := store(t)
	err := s.Promote("core", "imaginary", actor())
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if got, _, _ := s.DefaultFor("core"); got.Version != "" {
		t.Fatalf("a default was recorded anyway: %#v", got)
	}
}

// The decision is recorded next to the choice, because "why does this look
// like this" gets asked of the page rather than of the audit trail.
func TestPromotingRecordsWhoDecidedAndWhen(t *testing.T) {
	fixedClock(t)
	s := store(t)
	save(t, s, "core", "wide")
	if err := s.Promote("core", "wide", Actor{Name: "github:chief", Origin: Unverified}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.DefaultFor("core")
	if err != nil || !ok {
		t.Fatalf("no default: %v", err)
	}
	if got.By != "github:chief" || got.Origin != Unverified || got.Version != "wide" {
		t.Fatalf("%#v", got)
	}
	if got.At != "2026-08-31T12:00:00Z" {
		t.Fatalf("the time was not recorded: %q", got.At)
	}
}

// A name with no name behind it has to read as that, not as a missing field.
func TestAnUnnamedActorIsRecordedAsOne(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	if err := s.Promote("core", "wide", Actor{}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.DefaultFor("core")
	if got.By != Anonymous || got.Origin != Unverified {
		t.Fatalf("%#v", got)
	}
}

// Deleting the version somebody promoted has to take the pointer with it.
// Leaving it means the next drawing quietly comes out without the layout that
// was chosen, and nothing anywhere says why.
func TestForgettingTheDefaultVersionAlsoTakesBackTheDefault(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.DefaultFor("core"); ok {
		t.Fatal("the default outlived the version it named")
	}
	if got, _ := s.LayoutFor("core"); got != "" {
		t.Fatalf("a layout still came back: %q", got)
	}
}

// Deleting some other version must leave the default alone.
func TestForgettingAnotherVersionLeavesTheDefaultAlone(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	save(t, s, "core", "narrow")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget("core", "narrow", actor()); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.DefaultFor("core")
	if !ok || got.Version != "wide" {
		t.Fatalf("%#v", got)
	}
}

// Refusing to draw is no picture; drawing without the human layout is a worse
// picture. A worse picture beats no picture, so this stays quiet.
func TestADefaultWhoseFileWentMissingStillDraws(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	path, err := serve.Path(s.Root(), "core", "wide")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	got, err := s.LayoutFor("core")
	if err != nil {
		t.Fatalf("drawing was refused because a layout went missing: %v", err)
	}
	if got != "" {
		t.Fatalf("a path to a file that is not there: %q", got)
	}
}

// The quiet half above needs a loud half, or the page just comes out plain and
// everyone assumes that is what was asked for.
func TestADefaultWhoseFileWentMissingIsSaidOutLoud(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.StaleDefault("core"); got != "" {
		t.Fatalf("complained while the file was still there: %q", got)
	}

	path, _ := serve.Path(s.Root(), "core", "wide")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, err := s.StaleDefault("core")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wide" {
		t.Fatalf("expected to be told which one is missing, got %q", got)
	}
}

// Undoing something nobody did is not a change, and a journal is only worth
// reading if everything in it is one.
func TestTakingBackADefaultNobodySetChangesNothingAndSaysNothing(t *testing.T) {
	s := store(t)
	did, err := s.Demote("core", actor())
	if err != nil {
		t.Fatal(err)
	}
	if did {
		t.Fatal("claimed to have undone something")
	}
	if got, _ := s.History("", 0); len(got) != 0 {
		t.Fatalf("a non-event was recorded: %#v", got)
	}
}

// Saving a private version affects nobody. Recording it would bury the entries
// that do matter under entries that do not.
func TestOnlyChangesOtherPeopleSeeAreRecorded(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	if got, _ := s.History("", 0); len(got) != 0 {
		t.Fatalf("saving a version was recorded: %#v", got)
	}

	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	got, err := s.History("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != ActionPromote || got[0].Target != "core" {
		t.Fatalf("%#v", got)
	}
	if got[0].Actor != "github:someone" || got[0].Origin != Unverified {
		t.Fatalf("the actor and how well it is known did not both survive: %#v", got[0])
	}
}

// The question being asked of a journal is almost always "what just happened".
func TestTheJournalReadsNewestFirst(t *testing.T) {
	s := store(t)
	save(t, s, "core", "one")
	save(t, s, "core", "two")
	if err := s.Promote("core", "one", actor()); err != nil {
		t.Fatal(err)
	}
	if err := s.Promote("core", "two", actor()); err != nil {
		t.Fatal(err)
	}
	got, err := s.History("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%#v", got)
	}
	if got[0].Detail["version"] != "two" {
		t.Fatalf("the newest is not first: %#v", got)
	}
	if got, _ := s.History("", 1); len(got) != 1 || got[0].Detail["version"] != "two" {
		t.Fatalf("the limit did not keep the newest: %#v", got)
	}
}

func TestTheJournalCanBeAskedAboutOneSubject(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	save(t, s, "estate", "wide")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	if err := s.Promote("estate", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	got, err := s.History("estate", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Target != "estate" {
		t.Fatalf("%#v", got)
	}
}

// The file is appended to and a crash can tear the last line. One torn line
// must not hide every entry written before it.
func TestATornLineDoesNotHideTheEntriesBeforeIt(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root(), journalFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"at":"2026-08-31T12:00:00Z","act`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := s.History("", 0)
	if err != nil {
		t.Fatalf("a torn line ended the read: %v", err)
	}
	if len(got) != 1 || got[0].Action != ActionPromote {
		t.Fatalf("the good entry did not survive: %#v", got)
	}
}

// Replacing a whole document must not be able to destroy the previous good one
// by failing halfway.
func TestTheDefaultsFileIsReplacedWholeOrNotAtAll(t *testing.T) {
	s := store(t)
	save(t, s, "core", "wide")
	save(t, s, "estate", "tall")
	if err := s.Promote("core", "wide", actor()); err != nil {
		t.Fatal(err)
	}
	if err := s.Promote("estate", "tall", actor()); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(s.Root(), defaultsFile))
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]Default
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("what is on disk is not a whole document: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("%#v", all)
	}
	// Nothing left over from the write.
	entries, err := os.ReadDir(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// A name that could climb out of its folder has to be refused before it is
// used to build a path.
func TestANameCannotLeaveItsFolder(t *testing.T) {
	s := store(t)
	for _, bad := range []string{"../escape", "a/b", "", ".", "..", "with space"} {
		if err := s.Promote("core", bad, actor()); !errors.Is(err, ErrRefused) {
			t.Fatalf("version %q was not refused: %v", bad, err)
		}
		if _, err := s.Meta(bad); err == nil {
			t.Fatalf("item %q was not refused", bad)
		}
	}
}

// An annotation answers questions the drawing cannot, and stays true after the
// drawing is redrawn.
func TestWhatSomebodyWroteDownComesBack(t *testing.T) {
	s := store(t)
	in := Meta{Title: "the core", Note: "drawn from declared references only", Tags: []string{"estate"}}
	if _, err := s.Annotate("core", in, actor(), nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Meta("core")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != in.Title || got.Note != in.Note || len(got.Tags) != 1 {
		t.Fatalf("%#v", got)
	}
	if got.Claim == nil || got.Claim.SetBy != "github:someone" || got.Claim.Origin != Unverified {
		t.Fatalf("the claim was not stamped: %#v", got.Claim)
	}
}

// The failure at reading time is silence: the limit matches nobody, the item
// is hidden from everybody, and the person who typed the name saw a save that
// worked.
func TestALimitCannotNameARoleThatDoesNotExist(t *testing.T) {
	s := store(t)
	_, err := s.Annotate("core", Meta{ReadRoles: []string{"viewr"}}, actor(), []string{"viewer", "editor"})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "viewr") {
		t.Fatalf("the complaint does not name it: %v", err)
	}
	if got, _ := s.Meta("core"); got.Title != "" || len(got.ReadRoles) != 0 {
		t.Fatalf("it was written anyway: %#v", got)
	}
}

func TestALimitNamingARoleThatExistsIsKept(t *testing.T) {
	s := store(t)
	if _, err := s.Annotate("core", Meta{ReadRoles: []string{"editor"}}, actor(), []string{"viewer", "editor"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Meta("core")
	if len(got.ReadRoles) != 1 || got.ReadRoles[0] != "editor" {
		t.Fatalf("%#v", got)
	}
}

// Emptying every field is how a person deletes an annotation. The stamp saying
// who emptied it must not be the thing that keeps the document alive.
func TestEmptyingEveryFieldRemovesTheAnnotation(t *testing.T) {
	s := store(t)
	if _, err := s.Annotate("core", Meta{Title: "the core"}, actor(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Annotate("core", Meta{}, actor(), nil); err != nil {
		t.Fatal(err)
	}
	path, _ := s.metaPath("core")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file survived being emptied: %v", err)
	}
	if got, _ := s.AllMeta(); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}

func TestEverythingWrittenDownCanBeListedAtOnce(t *testing.T) {
	s := store(t)
	if _, err := s.Annotate("core", Meta{Title: "one"}, actor(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Annotate("estate", Meta{Title: "two"}, actor(), nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.AllMeta()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["core"].Title != "one" || got["estate"].Title != "two" {
		t.Fatalf("%#v", got)
	}
}

// Erasing nothing is not an error, and is not a change to record.
func TestErasingAnAnnotationNobodyWroteIsQuiet(t *testing.T) {
	s := store(t)
	if err := s.Erase("core", actor()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.History("", 0); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}
