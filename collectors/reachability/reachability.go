// Package reachability probes explicitly supplied endpoints and emits the
// same normalized evidence document consumed by enrichers/reachable.
//
// A probe only says what was reachable from the process that ran it. It must
// therefore carry an explicit source node; it is not a claim about every
// instance of that service or about the whole network.
package reachability

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Target struct {
	ID       string
	Address  string
	Protocol string
	Port     int
	Timeout  time.Duration
}

type Path struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Protocol string `json:"protocol,omitempty"`
	Port     int    `json:"port,omitempty"`
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason,omitempty"`
}

type Document struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Paths   []Path `json:"paths"`
}

// Probe checks every target from the explicitly named source. TCP is the
// default and is useful for service-to-service port reachability. HTTP and
// HTTPS perform a request only to the supplied address; the response status
// is recorded as reachable because an application-level response proves the
// connection completed even when it is a 4xx/5xx.
func Probe(ctx context.Context, from string, targets []Target) (Document, error) {
	if strings.TrimSpace(from) == "" {
		return Document{}, fmt.Errorf("probe source is empty")
	}
	if len(targets) == 0 {
		return Document{}, fmt.Errorf("probe requires at least one target")
	}
	d := Document{Kind: "oekaki.reachability", Version: "1", Paths: make([]Path, 0, len(targets))}
	for i, target := range targets {
		if strings.TrimSpace(target.ID) == "" || strings.TrimSpace(target.Address) == "" {
			return Document{}, fmt.Errorf("targets[%d] requires id and address", i)
		}
		protocol := strings.ToLower(strings.TrimSpace(target.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		if protocol != "tcp" && protocol != "http" && protocol != "https" {
			return Document{}, fmt.Errorf("targets[%d]: unsupported protocol %q", i, target.Protocol)
		}
		timeout := target.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		allowed, reason := probeOne(ctx, target, protocol, timeout)
		path := Path{From: from, To: target.ID, Protocol: protocol, Port: target.Port, Allowed: allowed, Reason: reason}
		d.Paths = append(d.Paths, path)
	}
	return d, nil
}

func probeOne(ctx context.Context, target Target, protocol string, timeout time.Duration) (bool, string) {
	switch protocol {
	case "http", "https":
		u, err := url.Parse(target.Address)
		if err != nil || strings.ToLower(u.Scheme) != protocol {
			return false, fmt.Sprintf("address scheme does not match protocol %q", protocol)
		}
		client := &http.Client{Timeout: timeout}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Address, nil)
		if err != nil {
			return false, "request creation failed: " + err.Error()
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, "request failed: " + err.Error()
		}
		resp.Body.Close()
		return true, fmt.Sprintf("HTTP %s responded", resp.Status)
	default:
		dialer := net.Dialer{Timeout: timeout}
		conn, err := dialer.DialContext(ctx, "tcp", target.Address)
		if err != nil {
			return false, "TCP connection failed: " + err.Error()
		}
		conn.Close()
		return true, "TCP connection succeeded"
	}
}

func (d Document) MarshalIndent() ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
