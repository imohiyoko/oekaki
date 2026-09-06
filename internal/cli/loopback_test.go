package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Binding loopback keeps the network out. It does not keep out the page in the
// tab next to this one: a name the attacker controls is made to resolve to
// 127.0.0.1 after the browser has loaded their page, and the request that
// follows is same-origin as far as the browser is concerned.
//
// The name is the thing that does not survive the trick. A browser sends the
// name it was given, and a script cannot change it.
func TestARequestThatDidNotComeToThisMachineIsRefused(t *testing.T) {
	s := testSite(t)

	for _, host := range []string{"evil.example", "evil.example:8080", "192.168.1.10:8080", ""} {
		r := httptest.NewRequest(http.MethodGet, "/layouts", nil)
		r.Host = host
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Errorf("a request that arrived as %q came back %d", host, w.Code)
		}
	}
}

// Every route, not only the ones that write. A drawing, a graph, the journal
// and who holds which role are all readable, and none of them had a check.
func TestTheNameIsCheckedOnEveryRoute(t *testing.T) {
	s := testSite(t)

	for _, path := range []string{"/", "/layouts", "/manage", "/roles", "/api/layouts/core/wide"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "evil.example"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Errorf("%s came back %d for a request that did not arrive here", path, w.Code)
		}
	}
}

// Everything a browser may put in a Host header for this machine.
func TestWhatCountsAsThisMachine(t *testing.T) {
	yes := []string{
		"localhost", "localhost:8080", "LOCALHOST:8080",
		"127.0.0.1", "127.0.0.1:8080", "127.0.0.53",
		"[::1]", "[::1]:8080",
	}
	for _, host := range yes {
		if !loopbackHost(host) {
			t.Errorf("%q is this machine and was refused", host)
		}
	}

	no := []string{
		"", "evil.example", "evil.example:8080", "192.168.1.10", "10.0.0.1:8080",
		// A name of your own pointed at 127.0.0.1 is refused too: the browser
		// sends the name, and a name is what the trick above changes.
		"oekaki.local", "localhost.evil.example",
	}
	for _, host := range no {
		if loopbackHost(host) {
			t.Errorf("%q is not this machine and was let through", host)
		}
	}
}

// And the page that was opened here still works, which is the whole point of
// refusing the other one.
func TestAPageOpenedHereIsStillServed(t *testing.T) {
	s := testSite(t)

	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		r := httptest.NewRequest(http.MethodGet, "/layouts", nil)
		r.Host = host
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("a page opened at %q came back %d: %s", host, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}
