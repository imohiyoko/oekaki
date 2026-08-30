package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/authz"
)

// write puts one file into one of the three directories.
func write(t *testing.T, dir, sub, name, body string) {
	t.Helper()
	full := filepath.Join(dir, sub)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const rolesHead = "kind: oekaki.roles\nversion: \"0.1\"\n"
const catalogHead = "kind: oekaki.catalog\nversion: \"0.1\"\n"

// A deployment that has written none of this yet should still start. Missing
// configuration and broken configuration are different things and only one of
// them should stop anything.
func TestNothingConfiguredIsNotAnError(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("an absent directory was an error: %v", err)
	}
	if len(got.Roles.Roles) != 0 || len(got.Catalog.Items) != 0 || got.Conventions != nil {
		t.Fatalf("%#v", got)
	}
}

// Being pointed at a path explicitly and finding nothing there is worth
// hearing about, as distinct from having chosen not to configure anything.
func TestBeingPointedAtAPathThatIsNotThereIsSaidOutLoud(t *testing.T) {
	_, err := Require(filepath.Join(t.TempDir(), "typo"))
	if err == nil {
		t.Fatal("a wrong path was accepted")
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Fatalf("the complaint does not name the path: %v", err)
	}
}

func TestRolesComeOutOfTheirOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "roles.yaml", rolesHead+`
roles:
  viewer:
    - {permission: read, effect: allow}
  editor:
    - {permission: read, effect: allow}
    - {permission: write, effect: allow}
anonymous: [viewer]
`)
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roles.Roles) != 2 {
		t.Fatalf("%#v", got.Roles.Roles)
	}
	if len(got.Roles.Anonymous) != 1 || got.Roles.Anonymous[0] != "viewer" {
		t.Fatalf("%#v", got.Roles.Anonymous)
	}
	got.Roles.Enforce = true
	if d := authz.Can(got.Roles, authz.Request{Permission: authz.Read}); !d.Allowed {
		t.Fatalf("the loaded policy does not work: %q", d.Because)
	}
}

// A file named so it sorts last is how somebody overrides the shared
// description without editing it.
func TestALaterFileWinsWhereTwoDisagree(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "10-shared.yaml", rolesHead+"roles:\n  viewer:\n    - {permission: read, effect: allow}\n")
	write(t, dir, RolesDir, "90-mine.yaml", rolesHead+"roles:\n  viewer:\n    - {permission: read, effect: deny}\n")
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roles.Roles["viewer"]) != 1 || got.Roles.Roles["viewer"][0].Effect != authz.Deny {
		t.Fatalf("the later file did not win: %#v", got.Roles.Roles["viewer"])
	}
}

// Half of one file's idea of a role and half of another's is not something
// anybody meant to write, so a role is replaced whole rather than added to.
func TestARoleIsReplacedWholeRatherThanAddedTo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "10-a.yaml", rolesHead+
		"roles:\n  worker:\n    - {permission: read, effect: allow}\n    - {permission: write, effect: allow}\n")
	write(t, dir, RolesDir, "20-b.yaml", rolesHead+
		"roles:\n  worker:\n    - {permission: read, effect: allow}\n")
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roles.Roles["worker"]) != 1 {
		t.Fatalf("the rules were merged instead of replaced: %#v", got.Roles.Roles["worker"])
	}
}

// A typo found when the program starts is a message; the same typo found later
// is somebody being refused something with no way to see why.
func TestATypoInARoleIsFoundAtStartup(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "roles.yaml", rolesHead+"roles:\n  viewer:\n    - {permission: reed, effect: allow}\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("an invented permission started up fine")
	}
	if !strings.Contains(err.Error(), "reed") {
		t.Fatalf("the complaint does not name it: %v", err)
	}
}

func TestAnAnonymousRoleThatDoesNotExistIsFoundAtStartup(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "roles.yaml", rolesHead+
		"roles:\n  viewer:\n    - {permission: read, effect: allow}\nanonymous: [viewr]\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("a misspelt anonymous role started up fine")
	}
}

// The shape is described once, as a schema, and enforced on the file people
// actually write.
func TestAFileThatIsNotTheRightShapeIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "roles.yaml", rolesHead+"roles: not-a-table\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("a malformed roles file was accepted")
	}

	other := t.TempDir()
	write(t, other, CatalogDir, "catalog.yaml", catalogHead+"items: 3\n")
	if _, err := Load(other); err == nil {
		t.Fatal("a malformed catalog was accepted")
	}
}

// An effect the evaluator does not have has to be caught by the schema, not
// silently treated as nothing.
func TestAnEffectOutsideTheTwoAllowedIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "roles.yaml", rolesHead+"roles:\n  viewer:\n    - {permission: read, effect: maybe}\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("an invented effect was accepted")
	}
}

func TestTheCatalogComesOutOfItsOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, CatalogDir, "catalog.yaml", catalogHead+`
title: what we have
kinds:
  - {id: drawing, label: Drawings}
items:
  - {match: "core.html", kind: drawing, title: the whole thing}
theme:
  ink: navy
`)
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Catalog.Title != "what we have" || got.Catalog.Theme["ink"] != "navy" {
		t.Fatalf("%#v", got.Catalog)
	}
	if e := got.Catalog.Describe("core.html"); e.Title != "the whole thing" || e.Label != "Drawings" {
		t.Fatalf("%#v", e)
	}
}

// The three directories are read independently, so a deployment can configure
// one without the others.
func TestConventionsComeOutOfTheirOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	head := "kind: oekaki.conventions\nversion: \"0.1\"\n"
	write(t, dir, ConventionsDir, "10-shared.yaml", head+"accountFromLocal: [deploy_account]\n")
	write(t, dir, ConventionsDir, "90-mine.yaml", head+"accountFromLocal: [target_account]\n")
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Conventions == nil || len(got.Conventions.AccountFromLocal) != 2 {
		t.Fatalf("%#v", got.Conventions)
	}
}

// A flag is in front of the person running the command and the environment is
// not, so the flag wins.
func TestWhereThingsLiveIsAskedForInOneOrder(t *testing.T) {
	t.Setenv(EnvConfig, "/from/env")
	if got := Dir("/from/flag"); got != "/from/flag" {
		t.Fatalf("the flag did not win: %q", got)
	}
	if got := Dir(""); got != "/from/env" {
		t.Fatalf("the environment was not read: %q", got)
	}
	os.Unsetenv(EnvConfig)
	if got := Dir(""); got != DefaultDir {
		t.Fatalf("the default did not apply: %q", got)
	}
}

func TestStateFallsBackToADirectoryBesideWhatIsServed(t *testing.T) {
	t.Setenv(EnvState, "/from/env")
	if got := StateDir("/from/flag", "/served"); got != "/from/flag" {
		t.Fatalf("%q", got)
	}
	if got := StateDir("", "/served"); got != "/from/env" {
		t.Fatalf("%q", got)
	}
	os.Unsetenv(EnvState)
	if got := StateDir("", "/served"); got != filepath.Join("/served", ".oekaki-state") {
		t.Fatalf("%q", got)
	}
}

// Who holds a role changes while the program runs and belongs with the state.
// A configuration file that could carry it would let one be shipped.
func TestAConfigurationFileCannotSayWhoHoldsARole(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "roles.yaml", rolesHead+
		"roles:\n  viewer:\n    - {permission: read, effect: allow}\ngrants:\n  github:someone: [viewer]\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("a roles file was allowed to name a holder")
	}
}

// A plain slice and a plain bool cannot say the difference between "I did not
// mention this" and "I am turning this off", and the second is how a personal
// file revokes what a shared file granted. Reading them the same way means the
// only direction configuration can move is looser.
func TestALaterFileCanTakeSomethingBackAndNotOnlyAddToIt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "10-shared.yaml", rolesHead+
		"roles:\n  viewer:\n    - {permission: read, effect: allow}\n"+
		"anonymous: [viewer]\nallowWithoutRepoRead: true\n")
	write(t, dir, RolesDir, "90-mine.yaml", rolesHead+
		"roles:\n  viewer:\n    - {permission: read, effect: allow}\n"+
		"anonymous: []\nallowWithoutRepoRead: false\n")

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roles.Anonymous) != 0 {
		t.Fatalf("anonymous could not be emptied: %#v", got.Roles.Anonymous)
	}
	if got.Roles.AllowWithoutRepoRead {
		t.Fatal("the repository floor could not be put back")
	}
}

// Saying nothing still has to leave what came before alone.
func TestAFileThatSaysNothingAboutSomethingLeavesItAlone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, RolesDir, "10-shared.yaml", rolesHead+
		"roles:\n  viewer:\n    - {permission: read, effect: allow}\n"+
		"anonymous: [viewer]\nallowWithoutRepoRead: true\n")
	write(t, dir, RolesDir, "90-mine.yaml", rolesHead+
		"roles:\n  editor:\n    - {permission: read, effect: allow}\n")

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roles.Anonymous) != 1 || !got.Roles.AllowWithoutRepoRead {
		t.Fatalf("%#v", got.Roles)
	}
}
