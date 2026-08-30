package tfsource

import (
	"os"
	"path/filepath"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func backend(key string) string {
	return "terraform {\n  backend \"s3\" {\n    bucket = \"b\"\n    key    = \"" + key + "\"\n  }\n}\n"
}

func remote(key string) string {
	return "data \"terraform_remote_state\" \"x\" {\n  backend = \"s3\"\n  config = {\n    key = \"" + key + "\"\n  }\n}\n"
}

// A root module is a directory that says where its state lives. Directories
// full of .tf files that do not are modules somebody calls, not roots.
func TestOnlyDirectoriesThatNameTheirStateAreRootModules(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app"),
		"shared/main.tf":  "resource \"aws_vpc\" \"v\" {}\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Key != "states/app" || mods[0].Dir != "app" {
		t.Fatalf("expected one root module, got %#v", mods)
	}
}

// Where the backend block sits is a matter of taste. A scanner that only reads
// provider.tf reports a smaller estate than exists and says nothing about it.
func TestTheBackendCanBeInAnyFile(t *testing.T) {
	root := tree(t, map[string]string{"app/anything.tf": backend("states/app")})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 {
		t.Fatalf("a backend outside provider.tf was missed: %#v", mods)
	}
}

// Vendored copies of other people's modules carry their own backend blocks.
// Counting those invents infrastructure nobody deployed.
func TestVendoredCopiesAreNotRootModules(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf":                        backend("states/app"),
		"app/.terraform/modules/dep/provider.tf": backend("states/somebody-else"),
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Key != "states/app" {
		t.Fatalf("a vendored module was counted: %#v", mods)
	}
}

// The edge is "reads": removing the module on the right breaks the one on the
// left.
func TestReadingAnotherModulesStateIsAnEdge(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") + remote("states/db"),
		"db/provider.tf":  backend("states/db"),
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := Graph(mods, "")
	if len(g.Edges) != 1 {
		t.Fatalf("expected one edge, got %#v", g.Edges)
	}
	if g.Edges[0].From != "module:states/app" || g.Edges[0].To != "module:states/db" {
		t.Fatalf("edge points the wrong way: %#v", g.Edges[0])
	}
}

// Terraform would fail on a reference to a state nothing writes. Dropping the
// edge would make the picture agree with a repository that does not work.
func TestAReferenceToNothingIsKeptAndMarked(t *testing.T) {
	root := tree(t, map[string]string{"app/provider.tf": backend("states/app") + remote("states/gone")})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := Graph(mods, "")
	if len(g.Edges) != 1 {
		t.Fatalf("the dangling edge was dropped: %#v", g.Edges)
	}
	var marked bool
	for _, n := range g.Nodes {
		if n.ID == "module:states/gone" && n.Attrs["unresolved"] == true {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("the missing module was not marked unresolved: %#v", g.Nodes)
	}
}

// The assume-role ARN names an account literally, so there is nothing to
// guess. Estates that record it some other way get no account rather than a
// wrong one: a wrong account draws a module inside somebody else's estate.
func TestAnAccountIsTakenFromTheRoleItAssumes(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") +
			"provider \"aws\" {\n  assume_role {\n    role_arn = \"arn:aws:iam::123456789012:role/deploy\"\n  }\n}\n",
		"quiet/provider.tf": backend("states/quiet"),
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range mods {
		got[m.Key] = m.Account
	}
	if got["states/app"] != "123456789012" {
		t.Fatalf("the account was not read: %#v", got)
	}
	if got["states/quiet"] != "" {
		t.Fatalf("an account was invented for a module that names none: %q", got["states/quiet"])
	}
}

// config = { key = "..." } on one line is as valid as the block form. A
// scanner that only reads one of them reports a smaller estate and says
// nothing about the difference.
func TestAKeyIsFoundOnOneLineToo(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") +
			"data \"terraform_remote_state\" \"db\" {\n  config = { key = \"states/db\" }\n}\n",
		"db/provider.tf": backend("states/db"),
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mods {
		if m.Key == "states/app" && len(m.Requires) == 1 && m.Requires[0] == "states/db" {
			return
		}
	}
	t.Fatalf("the one-line reference was missed: %#v", mods)
}

// An identifier that merely ends in key is not the key.
func TestAnAttributeEndingInKeyIsNotTheKey(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": "terraform {\n  backend \"s3\" {\n    workspace_key_prefix = \"nope\"\n    key    = \"states/app\"\n  }\n}\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Key != "states/app" {
		t.Fatalf("wrong key: %#v", mods)
	}
}

// Terraform Cloud names a workspace instead of a key, and it does so inside a
// nested block — the case a non-greedy regex reads half of.
func TestTerraformCloudNamesAWorkspace(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": "terraform {\n  cloud {\n    organization = \"acme\"\n    workspaces {\n      name = \"app-prod\"\n    }\n  }\n}\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Key != "app-prod" || mods[0].Backend != "cloud" {
		t.Fatalf("the workspace was not read: %#v", mods)
	}
}

// Each backend names its state with a different attribute. Reading key
// everywhere would join two estates on something they do not share.
func TestEachBackendNamesItsStateItsOwnWay(t *testing.T) {
	root := tree(t, map[string]string{
		"g/provider.tf": "terraform {\n  backend \"gcs\" {\n    bucket = \"b\"\n    prefix = \"states/g\"\n  }\n}\n",
		"a/provider.tf": "terraform {\n  backend \"azurerm\" {\n    key = \"states/a\"\n  }\n}\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range mods {
		got[m.Backend] = m.Key
	}
	if got["gcs"] != "states/g" || got["azurerm"] != "states/a" {
		t.Fatalf("backends read wrong: %#v", got)
	}
}

// A backend nobody taught this package about is reported, not skipped. A
// directory quietly left out looks the same as one that is not there.
func TestABackendItCannotReadIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"odd/provider.tf": "terraform {\n  backend \"cos\" {\n    key = \"states/odd\"\n  }\n}\n",
	})

	mods, unknown, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 0 {
		t.Fatalf("an unreadable backend was counted as a module: %#v", mods)
	}
	if len(unknown) != 1 || unknown[0].Backend != "cos" {
		t.Fatalf("the unreadable backend was not reported: %#v", unknown)
	}
}

// A reference says which backend it reads. Assuming the reader's own backend
// would join states that share nothing.
func TestAReferenceSaysWhichBackendItReads(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") +
			"data \"terraform_remote_state\" \"g\" {\n  backend = \"gcs\"\n  config = {\n    prefix = \"states/g\"\n  }\n}\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || len(mods[0].Requires) != 1 || mods[0].Requires[0] != "states/g" {
		t.Fatalf("the gcs reference was not read: %#v", mods)
	}
}

// Commented-out blocks are not infrastructure. Counting them draws references
// somebody deliberately took out, and the picture then disagrees with what
// Terraform would do.
func TestCommentedOutReferencesAreNotRead(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") + remote("states/live") +
			"# data \"terraform_remote_state\" \"old\" {\n#   config = {\n#     key = \"states/gone\"\n#   }\n# }\n" +
			"// data \"terraform_remote_state\" \"older\" {\n//   config = { key = \"states/older\" }\n// }\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 {
		t.Fatalf("expected one module: %#v", mods)
	}
	if len(mods[0].Requires) != 1 || mods[0].Requires[0] != "states/live" {
		t.Fatalf("commented-out references were read: %#v", mods[0].Requires)
	}
}

// Somebody stopped using a module and left the block behind. Counting it
// invents a root module nobody deploys.
func TestACommentedOutBackendIsNotAModule(t *testing.T) {
	root := tree(t, map[string]string{
		"gone/provider.tf": "# terraform {\n#   backend \"s3\" {\n    key = \"states/not-real\"\n# }\n",
		"live/provider.tf": backend("states/live"),
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Key != "states/live" {
		t.Fatalf("a commented-out backend was counted: %#v", mods)
	}
}

// Example configuration inside a heredoc is documentation, not a deployment.
func TestABackendInsideAHeredocIsNotAModule(t *testing.T) {
	root := tree(t, map[string]string{
		"doc/main.tf": "resource \"aws_instance\" \"x\" {\n  user_data = <<EOT\n" +
			"    terraform { backend \"s3\" { key = \"states/example\" } }\n  EOT\n}\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 0 {
		t.Fatalf("a heredoc's contents were read as configuration: %#v", mods)
	}
}

// A role ARN can appear anywhere — a policy attachment naming another
// account's role is ordinary. Reading those makes the wrong answer more likely
// the more thorough the module is, and a wrong account draws the module inside
// somebody else's estate.
func TestOnlyTheRoleTheProviderAssumesNamesTheAccount(t *testing.T) {
	root := tree(t, map[string]string{
		"app/provider.tf": backend("states/app") +
			"provider \"aws\" {\n  assume_role {\n    role_arn = \"arn:aws:iam::111111111111:role/deploy\"\n  }\n}\n" +
			"resource \"aws_iam_role_policy_attachment\" \"a\" {\n  role = \"arn:aws:iam::999999999999:role/other\"\n}\n" +
			"resource \"aws_iam_role_policy_attachment\" \"b\" {\n  role = \"arn:aws:iam::999999999999:role/other2\"\n}\n",
	})

	mods, _, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Account != "111111111111" {
		t.Fatalf("the account came from somewhere other than assume_role: %#v", mods)
	}
}

// Naming the estate has to qualify the ids, not only the metadata: two
// repositories can both hold a state called states/vpc.
func TestNamingTheEstateQualifiesTheIds(t *testing.T) {
	mods := []Module{{Key: "states/vpc", Account: "111111111111"}}

	g := Graph(mods, "prod")

	if g.Nodes[0].ID != "prod:module:states/vpc" {
		t.Fatalf("the node id was not qualified: %q", g.Nodes[0].ID)
	}
	if g.Groups[0].ID != "prod:111111111111" {
		t.Fatalf("the group id was not qualified: %q", g.Groups[0].ID)
	}
	if g.Nodes[0].Groups["account"] != "prod:111111111111" {
		t.Fatalf("the node's group reference was not qualified: %q", g.Nodes[0].Groups["account"])
	}
}
