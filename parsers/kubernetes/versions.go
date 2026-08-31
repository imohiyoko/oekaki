package kubernetes

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SupportedThrough is the Kubernetes release this table was last checked
// against. It is a floor, not a ceiling: a manifest written for a later
// release parses fine as long as it uses an apiVersion listed below, and one
// that uses something newer becomes a node with api_unknown set rather than
// being dropped. The constant exists so that "what does this understand?" has
// an answer that can be read, tested, and bumped deliberately.
const SupportedThrough = "1.37"

// API is one apiVersion and kind this parser recognises.
type API struct {
	// Group is the API group, empty for the core group. Version and Kind are
	// as written in a manifest, so Group+"/"+Version is the apiVersion field
	// except in the core group, where it is just the version.
	Group   string
	Version string
	Kind    string

	// Since is the Kubernetes release that started serving this apiVersion for
	// this kind. The highest Since across a document set is the oldest cluster
	// that can accept all of it, which is what Parse records as the graph's
	// source version.
	Since string

	// Removed is the release that stopped serving it, empty while it is still
	// served. An object using one of these still becomes a node: a manifest no
	// current cluster will accept is a finding, and dropping it hides the
	// finding rather than the problem.
	Removed string

	// Recovers names the relationships the parser reads out of this kind.
	// Empty means the object is drawn but nothing is read from it, which is a
	// different statement from "not supported" and is worth being able to tell
	// apart.
	Recovers []string
}

// APIVersion renders the apiVersion field this entry matches.
func (a API) APIVersion() string {
	if a.Group == "" {
		return a.Version
	}
	return a.Group + "/" + a.Version
}

// Served reports whether a cluster running the given release still serves this
// apiVersion.
func (a API) Served(release string) bool {
	return a.Removed == "" || compare(release, a.Removed) < 0
}

// table is the whole of what this parser claims to understand. Adding a kind
// means adding a row: the test refuses a kind the parser handles without one,
// and a row nothing handles.
var table = []API{
	{Version: "v1", Kind: "Namespace", Since: "1.0"},
	{Version: "v1", Kind: "Pod", Since: "1.0", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Version: "v1", Kind: "Service", Since: "1.0", Recovers: []string{"selector"}},
	{Version: "v1", Kind: "ConfigMap", Since: "1.0"},
	{Version: "v1", Kind: "Secret", Since: "1.0"},
	{Version: "v1", Kind: "PersistentVolumeClaim", Since: "1.0"},
	{Version: "v1", Kind: "ServiceAccount", Since: "1.0"},

	{Group: "apps", Version: "v1", Kind: "Deployment", Since: "1.9", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Group: "apps", Version: "v1", Kind: "StatefulSet", Since: "1.9", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Group: "apps", Version: "v1", Kind: "DaemonSet", Since: "1.9", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Group: "apps", Version: "v1", Kind: "ReplicaSet", Since: "1.9", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Group: "apps", Version: "v1beta2", Kind: "Deployment", Since: "1.8", Removed: "1.16", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Group: "apps", Version: "v1beta1", Kind: "Deployment", Since: "1.6", Removed: "1.16", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},

	{Group: "batch", Version: "v1", Kind: "Job", Since: "1.2", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Group: "batch", Version: "v1", Kind: "CronJob", Since: "1.21", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},
	{Group: "batch", Version: "v1beta1", Kind: "CronJob", Since: "1.8", Removed: "1.25", Recovers: []string{"configmap", "secret", "pvc", "serviceaccount", "owner"}},

	{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress", Since: "1.19", Recovers: []string{"backend"}},
	{Group: "networking.k8s.io", Version: "v1beta1", Kind: "Ingress", Since: "1.14", Removed: "1.22", Recovers: []string{"backend"}},
	{Group: "extensions", Version: "v1beta1", Kind: "Ingress", Since: "1.2", Removed: "1.22", Recovers: []string{"backend"}},

	{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler", Since: "1.23", Recovers: []string{"scaleTarget"}},
	{Group: "autoscaling", Version: "v1", Kind: "HorizontalPodAutoscaler", Since: "1.2", Recovers: []string{"scaleTarget"}},
	{Group: "autoscaling", Version: "v2beta2", Kind: "HorizontalPodAutoscaler", Since: "1.12", Removed: "1.26", Recovers: []string{"scaleTarget"}},
	{Group: "autoscaling", Version: "v2beta1", Kind: "HorizontalPodAutoscaler", Since: "1.8", Removed: "1.25", Recovers: []string{"scaleTarget"}},
}

// lookup finds the entry for an apiVersion and kind.
func lookup(apiVersion, kind string) (API, bool) {
	group, version := split(apiVersion)
	for _, a := range table {
		if a.Group == group && a.Version == version && a.Kind == kind {
			return a, true
		}
	}
	return API{}, false
}

// Table returns the recognised apiVersions, ordered for display.
func Table() []API {
	out := append([]API(nil), table...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		// The apiVersion still served comes first: it is the one a reader is
		// looking for, and the removed ones are there as a warning.
		if (out[i].Removed == "") != (out[j].Removed == "") {
			return out[i].Removed == ""
		}
		return out[i].Version > out[j].Version
	})
	return out
}

// split separates an apiVersion into group and version. The core group is
// written as a bare version, which is why this is not a plain Cut.
func split(apiVersion string) (group, version string) {
	if i := strings.Index(apiVersion, "/"); i >= 0 {
		return apiVersion[:i], apiVersion[i+1:]
	}
	return "", apiVersion
}

// compare orders two dotted releases numerically. Kubernetes releases are
// x.y, and "1.9" sorts before "1.16" only if the parts are read as numbers.
func compare(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := 0, 0
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Markdown renders the table as the block docs/kubernetes.md carries. The
// document is generated rather than written so that adding a kind cannot
// leave the documentation claiming something else; the test compares the two.
func Markdown() string {
	var b strings.Builder
	b.WriteString("| apiVersion | kind | since | removed in | recovers |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, a := range Table() {
		removed, recovers := "—", "—"
		if a.Removed != "" {
			removed = "**" + a.Removed + "**"
		}
		if len(a.Recovers) > 0 {
			recovers = "`" + strings.Join(a.Recovers, "`, `") + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
			a.APIVersion(), a.Kind, a.Since, removed, recovers)
	}
	return b.String()
}
