package kubernetes

// Manifests are read as maps rather than as a struct per kind. Twenty kinds
// would be twenty structs to say the same few things, and every one of them
// would have to be right about fields this parser never looks at. These four
// helpers walk a decoded document and return the zero value at the first step
// that is missing, so a manifest that omits an optional block reads as absent
// rather than as a panic.

// dig walks a path of map keys.
func dig(v any, path ...string) any {
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

// str reads a string at a path. A non-string, such as a port written as a
// number, reads as empty rather than as its formatting.
func str(v any, path ...string) string {
	s, _ := dig(v, path...).(string)
	return s
}

// num reads a number at a path. The bool separates "absent" from "zero",
// which for replicas is the difference between unspecified and scaled down.
func num(v any, path ...string) (float64, bool) {
	switch n := dig(v, path...).(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// seq reads a list at a path.
func seq(v any, path ...string) []any {
	s, _ := dig(v, path...).([]any)
	return s
}

// strMap reads a map of strings at a path, dropping entries whose value is not
// a string. Label values are strings in Kubernetes; anything else in that
// position is a manifest that would not apply.
func strMap(v any, path ...string) map[string]string {
	m, ok := dig(v, path...).(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, value := range m {
		if s, ok := value.(string); ok {
			out[k] = s
		}
	}
	return out
}
