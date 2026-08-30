// Command metricpoller scrapes a Prometheus-compatible endpoint and writes
// labelled observations for oekaki to consume.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/imohiyoko/oekaki/collectors/prometheus"
	"github.com/imohiyoko/oekaki/core"
)

type thresholds map[string]core.Threshold

func (t *thresholds) String() string { return "" }
func (t *thresholds) Set(spec string) error {
	metric, rest, ok := strings.Cut(spec, "=")
	if !ok || metric == "" {
		return fmt.Errorf("threshold must be metric=operator:value")
	}
	operator, value, ok := strings.Cut(rest, ":")
	if !ok || !map[string]bool{">": true, ">=": true, "<": true, "<=": true, "==": true, "!=": true}[operator] {
		return fmt.Errorf("threshold must be metric=operator:value")
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("threshold %q: %w", spec, err)
	}
	if *t == nil {
		*t = map[string]core.Threshold{}
	}
	(*t)[metric] = core.Threshold{Operator: operator, Value: v}
	return nil
}

func main() {
	endpoint := flag.String("endpoint", "", "Prometheus-compatible metrics endpoint")
	out := flag.String("output", "observations.json", "observation output path")
	interval := flag.Duration("interval", 1*time.Minute, "poll interval; 0 runs once")
	subject := flag.String("subject-label", "service", "label containing the oekaki node id")
	unit := flag.String("unit", "", "unit assigned to all samples")
	var ts thresholds
	flag.Var(&ts, "threshold", "metric=operator:value; repeatable, e.g. error_rate=>:0.05")
	flag.Parse()
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "metricpoller: --endpoint is required")
		os.Exit(2)
	}
	p := &prometheus.Poller{
		Endpoint: *endpoint,
		Options:  prometheus.Options{SubjectLabel: *subject, Unit: *unit, Thresholds: ts},
		Sink:     prometheus.JSONSink{Path: *out},
	}
	if err := p.Run(context.Background(), *interval); err != nil {
		fmt.Fprintf(os.Stderr, "metricpoller: %v\n", err)
		os.Exit(1)
	}
}
