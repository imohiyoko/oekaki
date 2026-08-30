package terraform

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// resourceDecl matches the opening line of a resource block. A real HCL parse
// would be more correct, but the goal here is only to turn a diagram into a
// link back to a line of code, and a wrong line number is a smaller cost than
// a second parser to maintain.
var resourceDecl = regexp.MustCompile(`^\s*resource\s+"([^"]+)"\s+"([^"]+)"`)

// scanSources reads the .tf files directly inside dir and maps each resource
// address to where it was declared.
//
// It does not recurse. Files in subdirectories usually belong to a module, and
// their resources carry a `module.<name>.` prefix that cannot be recovered from
// the file path alone; guessing would attach source locations to the wrong
// resources.
func scanSources(dir string) (map[string]*core.Source, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading source directory: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	out := map[string]*core.Source{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for line := 1; scanner.Scan(); line++ {
			m := resourceDecl.FindStringSubmatch(scanner.Text())
			if m == nil {
				continue
			}
			addr := m[1] + "." + m[2]
			// First declaration wins; a duplicate address is a Terraform
			// error anyway, and files are visited in a stable order.
			if _, exists := out[addr]; !exists {
				out[addr] = &core.Source{File: name, Line: line}
			}
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
	}

	return out, nil
}
