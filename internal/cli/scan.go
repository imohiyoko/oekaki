package cli

import (
	"flag"
	"fmt"
	"sort"

	"github.com/imohiyoko/oekaki/parsers/tfsource"
)

// runScan builds a graph from committed Terraform source.
//
// It is a separate command rather than another input shape for render because
// what it reads is different in kind: not a plan or a state, which describe
// what exists, but source, which describes what is declared. Keeping them
// apart means the output can be piped into everything else exactly as
// `oekaki graph` already is.
func runScan(env Env, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	output := fs.String("o", "", "write to this file instead of standard output")
	scope := fs.String("scope", "", "name this estate, so documents from several repositories can be combined")
	conventions := fs.String("conventions", "", "a YAML file saying where this repository keeps facts Terraform does not standardise")
	if err := parse(fs, args); err != nil {
		return err
	}
	var err error
	if fs.NArg() != 1 {
		return fmt.Errorf("scan takes one directory")
	}

	var conv *tfsource.Conventions
	if *conventions != "" {
		if conv, err = tfsource.ReadConventions(*conventions); err != nil {
			return err
		}
	}

	mods, unknown, err := tfsource.Scan(fs.Arg(0), conv)
	if err != nil {
		return err
	}

	// Say what was found on every run. A scan that reads the wrong directory,
	// or one where the backend keys are set at init time rather than written
	// down, produces an empty graph and no error; the count is the only thing
	// that separates that from an estate with nothing in it.
	edges, placed := 0, 0
	for _, m := range mods {
		edges += len(m.Requires)
		if m.Account != "" {
			placed++
		}
	}
	// References to a key nothing declares are worth naming: Terraform would
	// fail on them too, so either the scan missed a module or the reference is
	// wrong. Neither is visible in the picture.
	declared := make(map[string]bool, len(mods))
	for _, m := range mods {
		declared[m.Key] = true
	}
	dangling := map[string]bool{}
	for _, m := range mods {
		for _, want := range m.Requires {
			if !declared[want] {
				dangling[want] = true
			}
		}
	}
	fmt.Fprintf(env.Stderr, "scanned %s: %d root modules, %d references, %d modules name an account\n",
		fs.Arg(0), len(mods), edges, placed)
	// A backend this package has not been shown is a directory left out, and a
	// directory left out looks exactly like one that is not there.
	if len(unknown) > 0 {
		kinds := map[string]int{}
		for _, u := range unknown {
			kinds[u.Backend]++
		}
		for _, k := range sortedKeys(kinds) {
			fmt.Fprintf(env.Stderr, "  %d modules use the %s backend, which this cannot read\n", kinds[k], k)
		}
	}
	if len(dangling) > 0 {
		fmt.Fprintf(env.Stderr, "  %d references point at a key nothing here declares\n", len(dangling))
	}

	out, err := tfsource.Graph(mods, *scope).MarshalIndent()
	if err != nil {
		return err
	}
	return write(env, *output, out)
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
