package tfsource

import (
	"os"
	"path/filepath"
	"testing"
)

func conventions(t *testing.T, body string) *Conventions {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadConventions(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const head = "kind: oekaki.conventions\nversion: \"0.1\"\n"

// The commonest way an estate names its account is a local, and Terraform has
// no opinion about what it is called.
func TestAnAccountCanComeFromANamedLocal(t *testing.T) {
	c := conventions(t, head+"accountFromLocal: [deploy_account]\n")
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") + "locals {\n  deploy_account = \"210987654321\"\n}\n",
	})

	mods, _, err := Scan(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Account != "210987654321" {
		t.Fatalf("the local was not read: %#v", mods)
	}
}

// The provider block always wins: it names the account with nothing to
// interpret, and a convention is a description of where else to look.
func TestTheProviderStillWinsOverAConvention(t *testing.T) {
	c := conventions(t, head+"accountFromLocal: [deploy_account]\n")
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") +
			"locals {\n  deploy_account = \"210987654321\"\n}\n" +
			"provider \"aws\" {\n  assume_role {\n    role_arn = \"arn:aws:iam::123456789012:role/deploy\"\n  }\n}\n",
	})

	mods, _, err := Scan(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if mods[0].Account != "123456789012" {
		t.Fatalf("a convention overrode what the provider says: %q", mods[0].Account)
	}
}

// A table with one row needs nobody to say which row.
func TestATableOfOneNeedsNoChoice(t *testing.T) {
	c := conventions(t, head+"accountTable:\n  variable: accounts\n")
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") +
			"locals {\n  accounts = {\n    only = \"210987654321\"\n  }\n}\n",
	})

	mods, _, err := Scan(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if mods[0].Account != "210987654321" {
		t.Fatalf("the single row was not taken: %q", mods[0].Account)
	}
}

// With more than one row, the module has to say which it takes.
func TestATableOfManyIsReadThroughTheRowTheModuleNames(t *testing.T) {
	c := conventions(t, head+"accountTable:\n  variable: accounts\n")
	src := "locals {\n  accounts = {\n    prod = \"210987654321\"\n    dev  = \"310987654321\"\n  }\n}\n"

	chosen := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") + src +
			"provider \"aws\" {\n  region = local.accounts.dev\n}\n",
	})
	mods, _, err := Scan(chosen, c)
	if err != nil {
		t.Fatal(err)
	}
	if mods[0].Account != "310987654321" {
		t.Fatalf("the named row was not taken: %q", mods[0].Account)
	}

	// Nothing names a row. Picking one would be right sometimes, and the times
	// it is wrong draw the module inside another account's estate.
	quiet := tree(t, map[string]string{"app/provider.tf": backend("states/app") + src})
	mods, _, err = Scan(quiet, c)
	if err != nil {
		t.Fatal(err)
	}
	if mods[0].Account != "" {
		t.Fatalf("a row was picked with nothing to pick it by: %q", mods[0].Account)
	}
}

// The file is written by hand, so it is checked before it is believed.
func TestAConventionsFileIsCheckedAgainstTheSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(head+"accountFromLocal: not-a-list\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConventions(path); err == nil {
		t.Fatal("a conventions file with the wrong shape was accepted")
	}
}

// No conventions at all is the ordinary case and must not need a file.
func TestNoConventionsIsFine(t *testing.T) {
	var none *Conventions
	if got := none.account("locals { deploy_account = \"210987654321\" }"); got != "" {
		t.Fatalf("something was read without being asked for: %q", got)
	}
}

// An account id is exactly twelve digits. A longer run of digits is not an
// account with something appended to it — it is a different value entirely,
// and returning its first twelve puts the module in an estate that does not
// exist.
func TestALongerRunOfDigitsIsNotAnAccount(t *testing.T) {
	c := conventions(t, head+"accountFromLocal: [deploy_account]\n")
	for _, body := range []string{
		"locals {\n  deploy_account = \"1234567890123\"\n}\n",
		"locals {\n  deploy_account = 1234567890123\n}\n",
	} {
		if got := c.account(body); got != "" {
			t.Errorf("read %q as an account from %q", got, body)
		}
	}
}

func TestALongerRunOfDigitsIsNotAnAccountInATableEither(t *testing.T) {
	c := conventions(t, head+"accountTable:\n  variable: accounts\n")
	body := "locals {\n  accounts = {\n    only = \"1234567890123\"\n  }\n}\n"
	if got := c.account(body); got != "" {
		t.Errorf("read %q as an account from a table row", got)
	}
}
