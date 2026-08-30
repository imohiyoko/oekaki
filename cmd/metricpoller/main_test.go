package main

import "testing"

func TestThresholdsRejectUnknownOperators(t *testing.T) {
	for _, spec := range []string{"latency=between:10", "latency=:10", "latency=>10", "latency=>:not-a-number"} {
		var thresholds thresholds
		if err := thresholds.Set(spec); err == nil {
			t.Errorf("threshold %q was accepted", spec)
		}
	}
}

func TestThresholdsAcceptSupportedOperators(t *testing.T) {
	for _, operator := range []string{">", ">=", "<", "<=", "==", "!="} {
		var thresholds thresholds
		if err := thresholds.Set("latency=" + operator + ":10"); err != nil {
			t.Errorf("threshold operator %q was rejected: %v", operator, err)
		}
	}
}
