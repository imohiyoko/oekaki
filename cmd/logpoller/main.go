// Command logpoller polls a mounted log directory and writes classified log
// metadata. Backend-specific stores can use the same collectors/loginventory
// Poller from their own command without changing the graph tool.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/collectors/loginventory"
)

type rules []string

func (r *rules) String() string     { return strings.Join(*r, ",") }
func (r *rules) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	root := flag.String("root", "", "mounted log directory containing JSONL records")
	out := flag.String("output", "log-inventory.json", "inventory output path")
	interval := flag.Duration("interval", 5*time.Minute, "poll interval; 0 runs once")
	var specs rules
	flag.Var(&specs, "rule", "classification rule label=regular_expression; repeatable")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "logpoller: --root is required")
		os.Exit(2)
	}
	var rs []loginventory.Rule
	for _, spec := range specs {
		parts := strings.SplitN(spec, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "logpoller: invalid --rule %q\n", spec)
			os.Exit(2)
		}
		re, err := regexp.Compile(parts[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "logpoller: %q: %v\n", spec, err)
			os.Exit(2)
		}
		rs = append(rs, loginventory.Rule{Label: parts[0], Pattern: re})
	}
	p := &loginventory.Poller{Store: loginventory.DirectoryStore{Root: *root}, Classifier: loginventory.RuleClassifier{Rules: rs}, Sink: loginventory.JSONSink{Path: *out}}
	if err := p.Run(context.Background(), *interval); err != nil {
		fmt.Fprintf(os.Stderr, "logpoller: %v\n", err)
		os.Exit(1)
	}
}
