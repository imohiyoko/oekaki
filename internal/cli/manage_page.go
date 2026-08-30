package cli

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/imohiyoko/oekaki/authz"
	"github.com/imohiyoko/oekaki/internal/serve"
	"github.com/imohiyoko/oekaki/manage"
)

// The look is one stylesheet with its colours in custom properties, so that a
// deployment can change them from its catalog without this file knowing
// anything about what it will be changed to.
const baseCSS = `body{margin:0;padding:32px 24px;font:14px/1.7 system-ui,sans-serif;color:var(--ink);background:var(--page);max-width:900px}
h1{font-size:20px;margin:0 0 4px}h2{font-size:15px;margin:32px 0 8px}
a{color:var(--accent)}.m{color:var(--muted)}small{color:var(--muted)}
table{width:100%;border-collapse:collapse;background:var(--surface);border-radius:8px;overflow:hidden;margin:8px 0}
th,td{text-align:left;padding:7px 12px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--muted);font-weight:400}tr:last-child td{border-bottom:0}
nav{margin:0 0 24px}nav a{margin-right:16px}
button{font:inherit;padding:2px 10px;border:1px solid var(--line);border-radius:6px;background:var(--surface);color:var(--ink);cursor:pointer}
.warn{background:var(--surface);border-left:3px solid var(--accent);padding:8px 12px;margin:8px 0}
code{background:var(--surface);padding:1px 5px;border-radius:4px}`

// theme is the fallback for every custom property the stylesheet uses. A
// deployment overrides what it cares about and inherits the rest, so a catalog
// naming one colour does not leave the others undefined.
var theme = map[string]string{
	"ink":     "#323232",
	"muted":   "#6e6b6b",
	"line":    "#e9e7e7",
	"surface": "#ffffff",
	"page":    "#f7f5f5",
	"accent":  "#285ac8",
}

func (s *site) css() string {
	var b strings.Builder
	b.WriteString("<style>:root{")
	names := make([]string, 0, len(theme))
	for k := range theme {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := theme[k]
		if s.cfg != nil && s.cfg.Catalog != nil {
			if over, ok := s.cfg.Catalog.Theme[k]; ok {
				v = over
			}
		}
		fmt.Fprintf(&b, "--%s:%s;", k, cssValue(v))
	}
	b.WriteString("}")
	b.WriteString(baseCSS)
	b.WriteString("</style>")
	return b.String()
}

// cssValue keeps a configured colour from closing the element it sits in. The
// catalog is a file somebody in the deployment wrote, which is not the same as
// a file this program wrote.
func cssValue(v string) string {
	v = strings.NewReplacer("<", "", ">", "", ";", "", "}", "", "{", "", `"`, "").Replace(v)
	if len(v) > 64 {
		v = v[:64]
	}
	return v
}

func (s *site) chrome(b *strings.Builder, title string) {
	name := title
	if s.cfg != nil && s.cfg.Catalog != nil && s.cfg.Catalog.Title != "" {
		name = s.cfg.Catalog.Title + " — " + title
	}
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>` + html.EscapeString(name) + `</title>`)
	b.WriteString(s.css())
	b.WriteString(`<nav><a href="/">pages</a><a href="/layouts">layouts</a>` +
		`<a href="/manage">manage</a><a href="/roles">roles</a></nav>`)
	b.WriteString(`<h1>` + html.EscapeString(title) + `</h1>`)
}

func (s *site) send(w http.ResponseWriter, b *strings.Builder) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, b.String())
}

// manage shows what is saved, which of it is current, and what was done.
//
// It is built on every request rather than generated once. A page listing
// things that can be deleted, written when they still existed, tells lies for
// as long as somebody leaves the tab open.
func (s *site) manage(w http.ResponseWriter, r *http.Request) {
	if d := s.may(r, authz.Read, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	pages, err := serve.Pages(s.pages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	s.chrome(&b, "what is saved")

	names := make([]string, 0, len(pages))
	for _, p := range pages {
		names = append(names, p.Rel)
	}
	described := s.cfg.Catalog.List(names)
	byRel := map[string]serve.Page{}
	for _, p := range pages {
		byRel[p.Rel] = p
	}

	if len(described) == 0 {
		b.WriteString(`<p class=m>No rendered pages under this directory.</p>`)
	}
	for _, e := range described {
		p := byRel[e.Name]
		b.WriteString(`<h2><a href="/` + html.EscapeString(p.Rel) + `">` + html.EscapeString(e.Title) + `</a>`)
		if e.Label != "" {
			b.WriteString(` <small>` + html.EscapeString(e.Label) + `</small>`)
		}
		b.WriteString(`</h2>`)
		if e.About != "" {
			b.WriteString(`<p class=m>` + html.EscapeString(e.About) + `</p>`)
		}

		if stale, err := s.store.StaleDefault(p.Name); err == nil && stale != "" {
			b.WriteString(`<p class=warn>` + html.EscapeString(p.Name) +
				` is set to be drawn with <code>` + html.EscapeString(stale) +
				`</code>, which is not saved any more. It is being drawn as generated.</p>`)
		}

		current := ""
		if d, ok, err := s.store.DefaultFor(p.Name); err == nil && ok {
			current = d.Version
			b.WriteString(`<p class=m>drawn with <code>` + html.EscapeString(d.Version) +
				`</code>, set by ` + html.EscapeString(d.By) + ` (` + html.EscapeString(d.Origin) +
				`) at ` + html.EscapeString(d.At) + `</p>`)
		}

		saved, err := serve.Layouts(s.pages, s.state, p)
		if err != nil {
			b.WriteString(`<p class=m>` + html.EscapeString(err.Error()) + `</p>`)
			continue
		}
		if len(saved) == 0 {
			b.WriteString(`<p class=m>Nothing saved for this one yet.</p>`)
			continue
		}
		b.WriteString(`<table><tr><th>saved<th>positions<th>placed<th>not in this graph<th>`)
		for _, l := range saved {
			href := "/" + p.Rel + "?layout=" + url.QueryEscape(l.Name)
			if l.Paired {
				href += "&overlay=" + url.QueryEscape(l.Name)
			}
			missing := "—"
			if n := len(l.Missing); n > 0 {
				missing = fmt.Sprintf("%d (%s)", n, strings.Join(l.Missing[:min(3, n)], ", "))
			}
			mark := ""
			if l.Name == current {
				mark = ` <small>current</small>`
			}
			b.WriteString(`<tr><td><a href="` + html.EscapeString(href) + `">` +
				html.EscapeString(l.Name) + `</a>` + mark +
				`<td>` + fmt.Sprint(l.Nodes) + `<td>` + fmt.Sprint(l.Placed) +
				`<td>` + html.EscapeString(missing) + `<td>`)
			if l.Name == current {
				b.WriteString(`<button data-method="DELETE" data-to="/api/defaults/` +
					html.EscapeString(url.PathEscape(p.Name)) + `/">draw as generated</button> `)
			} else {
				b.WriteString(`<button data-method="POST" data-to="/api/defaults/` +
					html.EscapeString(url.PathEscape(p.Name)) + `/` +
					html.EscapeString(url.PathEscape(l.Name)) + `">draw with this</button> `)
			}
			b.WriteString(`<button data-method="DELETE" data-to="/api/layouts/` +
				html.EscapeString(url.PathEscape(p.Name)) + `/` +
				html.EscapeString(url.PathEscape(l.Name)) + `">delete</button>`)
		}
		b.WriteString(`</table>`)
	}

	b.WriteString(`<h2>what was done</h2>`)
	entries, err := s.store.History("", 50)
	if err != nil {
		b.WriteString(`<p class=m>` + html.EscapeString(err.Error()) + `</p>`)
	} else if len(entries) == 0 {
		b.WriteString(`<p class=m>Nothing yet. Saving a version is not recorded here: ` +
			`it changes nothing for anybody else. Making one the default is.</p>`)
	} else {
		b.WriteString(`<table><tr><th>when<th>who<th>did<th>to<th>`)
		for _, e := range entries {
			b.WriteString(`<tr><td>` + html.EscapeString(e.At) +
				`<td>` + html.EscapeString(e.Actor) + ` <small>` + html.EscapeString(e.Origin) + `</small>` +
				`<td>` + html.EscapeString(e.Action) + `<td>` + html.EscapeString(e.Target) +
				`<td>` + html.EscapeString(detail(e)))
		}
		b.WriteString(`</table>`)
	}

	b.WriteString(actScript)
	s.send(w, &b)
}

func detail(e manage.Entry) string {
	if len(e.Detail) == 0 {
		return ""
	}
	keys := make([]string, 0, len(e.Detail))
	for k := range e.Detail {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %v", k, e.Detail[k]))
	}
	return strings.Join(parts, ", ")
}

// rolesPage shows what roles exist, who holds them, and what would change if
// this deployment started enforcing.
func (s *site) rolesPage(w http.ResponseWriter, r *http.Request) {
	if d := s.may(r, authz.Read, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	policy := s.policy()

	var b strings.Builder
	s.chrome(&b, "who may see what")

	if !policy.Enforce {
		b.WriteString(`<p class=warn>Nothing here is being enforced. This is running ` +
			`in local mode, which authorizes nobody. The table at the bottom is what ` +
			`would happen if it were.</p>`)
	}

	b.WriteString(`<h2>roles</h2>`)
	if len(policy.Roles) == 0 {
		b.WriteString(`<p class=m>No roles are configured. They are written by hand and ` +
			`shipped with the deployment, under <code>` +
			html.EscapeString(s.cfg.Dir) + `/roles</code>.</p>`)
	} else {
		b.WriteString(`<table><tr><th>role<th>may<th>held by`)
		holders := map[string][]string{}
		for subject, roles := range policy.Grants {
			for _, role := range roles {
				holders[role] = append(holders[role], subject)
			}
		}
		for _, name := range s.roleNames() {
			var says []string
			for _, rule := range policy.Roles[name] {
				says = append(says, string(rule.Effect)+" "+rule.Permission)
			}
			who := holders[name]
			sort.Strings(who)
			held := "nobody"
			if len(who) > 0 {
				held = strings.Join(who, ", ")
			}
			b.WriteString(`<tr><td>` + html.EscapeString(name) +
				`<td>` + html.EscapeString(strings.Join(says, ", ")) +
				`<td>` + html.EscapeString(held))
		}
		b.WriteString(`</table>`)
	}

	b.WriteString(`<h2>what each permission means</h2><table><tr><th>permission<th>needs<th>`)
	for _, p := range authz.Catalog() {
		needs := "—"
		if p.Parent != "" {
			needs = p.Parent
		}
		b.WriteString(`<tr><td>` + html.EscapeString(p.Name) +
			`<td>` + html.EscapeString(needs) + `<td>` + html.EscapeString(p.About))
	}
	b.WriteString(`</table><p class=m>These are the names this program checks for, so they ` +
		`are fixed here rather than configured. A permission nothing looks for would be a ` +
		`word promising a protection that does not exist.</p>`)

	b.WriteString(`<h2>who holds what</h2>`)
	b.WriteString(`<table><tr><th>subject<th>roles<th>`)
	subjects := make([]string, 0, len(policy.Grants))
	for s := range policy.Grants {
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)
	for _, subject := range subjects {
		b.WriteString(`<tr><td>` + html.EscapeString(subject) +
			`<td>` + html.EscapeString(strings.Join(policy.Grants[subject], ", ")) +
			`<td><button data-method="DELETE" data-to="/api/grants/` +
			html.EscapeString(url.PathEscape(subject)) + `">take away</button>`)
	}
	b.WriteString(`</table>`)
	b.WriteString(`<p class=m>Anonymous holds ` +
		html.EscapeString(orNone(strings.Join(policy.Anonymous, ", "))) +
		`. A subject is written <code>provider:name</code>, because a bare login means ` +
		`two different people the moment there are two providers.</p>`)

	items := map[string]authz.Item{}
	all, metaErr := s.store.AllMeta()
	if metaErr != nil {
		// The whole point of this page is showing what would be hidden before
		// anybody switches enforcement on. Swallowing this shows an empty set
		// of limits, which reads as "nobody loses anything" — the exact
		// conclusion the paragraph at the bottom exists to prevent.
		b.WriteString(`<p class=warn>What people wrote down could not be read, so ` +
			`the limits below are incomplete and the table after them is not to be ` +
			`trusted: ` + html.EscapeString(metaErr.Error()) + `</p>`)
	}
	for name, m := range all {
		items[name] = authz.Item{ReadRoles: m.ReadRoles}
	}
	if len(items) > 0 {
		b.WriteString(`<h2>items somebody limited</h2><table><tr><th>item<th>only for`)
		limited := make([]string, 0, len(items))
		for name, it := range items {
			if len(it.ReadRoles) > 0 {
				limited = append(limited, name)
			}
		}
		sort.Strings(limited)
		for _, name := range limited {
			b.WriteString(`<tr><td>` + html.EscapeString(name) +
				`<td>` + html.EscapeString(strings.Join(items[name].ReadRoles, ", ")))
		}
		b.WriteString(`</table>`)
	}

	b.WriteString(`<h2>what would happen if this were enforced</h2>`)
	rows := authz.Explain(policy, items, nil)
	b.WriteString(`<table><tr><th>subject<th>roles<th>could see<th>could not`)
	for _, row := range rows {
		hidden := "—"
		if len(row.Hidden) > 0 {
			hidden = strings.Join(row.Hidden, ", ")
		}
		b.WriteString(`<tr><td>` + html.EscapeString(row.Subject) +
			`<td>` + html.EscapeString(orNone(strings.Join(row.Roles, ", "))) +
			`<td>` + fmt.Sprint(row.Visible) + `<td>` + html.EscapeString(hidden))
	}
	b.WriteString(`</table>`)
	b.WriteString(`<p class=m>Turning enforcement on without looking at this either hides ` +
		`everything from everyone or protects nothing, and both get discovered by the ` +
		`people affected rather than by whoever turned it on.</p>`)

	b.WriteString(actScript)
	s.send(w, &b)
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

// actScript is the whole client side: one listener, one fetch, reload on
// success and show the sentence on failure. Every refusal here comes back as
// prose meant for a person, so there is nothing to interpret.
const actScript = `<script>
document.addEventListener('click', async (e) => {
  const el = e.target.closest('[data-to]');
  if (!el) return;
  const res = await fetch(el.dataset.to, {method: el.dataset.method || 'POST'});
  const text = await res.text();
  if (res.ok) location.reload(); else alert(text);
});
</script>`
