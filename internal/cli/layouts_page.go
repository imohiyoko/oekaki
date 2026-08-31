package cli

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/imohiyoko/oekaki/authz"
	"github.com/imohiyoko/oekaki/catalog"
	"github.com/imohiyoko/oekaki/internal/serve"
	"github.com/imohiyoko/oekaki/manage"
)

// The values state and fit take. They are named here because they travel in a
// url, get written into a file when somebody keeps a screening, and are read
// back by a later build — three places for a typo to survive in.
const (
	statePromoted = "promoted"
	statePlain    = "plain"
	stateStale    = "stale"

	fitComplete = "complete"
	fitPartial  = "partial"
	fitNone     = "none"
)

// What a single condition may be. A screening arrives in a url and can be
// written into a file by hand, so nothing that reaches the matching or the
// form is allowed to be unbounded.
const (
	conditionMax = 128
	tagsMax      = 16
)

// screen is what somebody asked to be shown of the listing.
//
// The listing is long for the same reason the pipeline is worth running: one
// generation per run, and the pages pile up. What separates the interesting
// ones from the rest is already recorded — who looks after this, what it was
// tagged, whether anybody settled on a version, whether that version still
// lands — and none of it was reachable except by reading the whole page.
type screen struct {
	Text  string   // anywhere in what is attached to the page
	Tags  []string // every one of them, not any
	Who   string   // maintainer, or whoever wrote it down
	Kind  string   // the catalog's grouping
	State string   // one of statePromoted, statePlain, stateStale
	Fit   string   // one of fitComplete, fitPartial, fitNone
}

// screenFrom reads the conditions out of a query string, keeping only what
// this page knows how to apply.
//
// Every caller goes through here, including the one reading a screening
// somebody kept months ago. That is the point: what is stored is a string a
// person typed, and it is narrowed to the conditions this build understands
// rather than carried into a link unread. A condition that has since been
// renamed is dropped and the listing widens, which is visible; a string put
// into a link unexamined would not be.
func screenFrom(q url.Values) screen {
	sc := screen{
		Text:  clip(q.Get("q")),
		Who:   clip(q.Get("who")),
		Kind:  clip(q.Get("kind")),
		State: oneOf(q.Get("state"), statePromoted, statePlain, stateStale),
		Fit:   oneOf(q.Get("fit"), fitComplete, fitPartial, fitNone),
	}
	seen := map[string]bool{}
	for _, raw := range q["tag"] {
		// A person typing two tags separates them the way they separate words.
		// Accepting only repeated parameters would work for the form and be a
		// surprise for anybody editing the url.
		for _, t := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		}) {
			t = clip(t)
			if t == "" || seen[strings.ToLower(t)] || len(sc.Tags) >= tagsMax {
				continue
			}
			seen[strings.ToLower(t)] = true
			sc.Tags = append(sc.Tags, t)
		}
	}
	return sc
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > conditionMax {
		s = strings.TrimSpace(s[:conditionMax])
	}
	return s
}

// oneOf keeps a value only if it is one this page acts on. Anything else
// becomes no condition at all, so a misspelt url shows everything rather than
// nothing — an empty listing reads as "there is nothing here", which would be
// a lie told by a typo.
func oneOf(v string, allowed ...string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return ""
}

func (sc screen) empty() bool {
	return sc.Text == "" && len(sc.Tags) == 0 && sc.Who == "" &&
		sc.Kind == "" && sc.State == "" && sc.Fit == ""
}

// values is the canonical form: the conditions this page understood, and
// nothing else that arrived beside them.
func (sc screen) values() url.Values {
	v := url.Values{}
	for key, at := range map[string]string{"q": sc.Text, "who": sc.Who,
		"kind": sc.Kind, "state": sc.State, "fit": sc.Fit} {
		if at != "" {
			v.Set(key, at)
		}
	}
	for _, t := range sc.Tags {
		v.Add("tag", t)
	}
	return v
}

// href is where this screening lives.
func (sc screen) href() string {
	if sc.empty() {
		return "/layouts"
	}
	return "/layouts?" + sc.values().Encode()
}

// row is one page and everything a condition can be asked about it.
type row struct {
	page  serve.Page
	entry catalog.Entry
	meta  manage.Meta

	// current is the version the page is drawn with; stale is that same name
	// when the file it points at has gone. They are kept apart because
	// "somebody settled this" and "what they settled is still honoured" are
	// different answers, and a screening that could not tell them apart would
	// file a page nobody has looked at since the file vanished in with the
	// settled ones.
	current string
	stale   string

	saved   []serve.Layout
	trouble string // why the above is less than the whole truth
}

// gather reads everything attached to a page, and remembers what it could not
// read rather than treating it as absent.
//
// An unreadable annotation is the difference between a page that has no tags
// and a page whose tags cannot be seen, and a screening on tags puts those two
// in the same place — out of the listing, with nothing saying so.
func (s *site) gather(p serve.Page) row {
	out := row{page: p, entry: s.cfg.Catalog.Describe(p.Rel)}
	if out.entry.Name == "" {
		// The catalog asks for this one to be left out of its own listing.
		// This page is not that listing: it is what is saved, and a page
		// vanishing from it because somebody tidied a heading elsewhere would
		// hide the layouts with it. Describe it as itself instead.
		out.entry = catalog.Entry{Name: p.Rel, Title: p.Rel}
	}
	var trouble []string
	if m, err := s.store.Meta(p.Name); err != nil {
		trouble = append(trouble, err.Error())
	} else {
		out.meta = m
	}
	if d, ok, err := s.store.DefaultFor(p.Name); err != nil {
		trouble = append(trouble, err.Error())
	} else if ok {
		out.current = d.Version
	}
	if stale, err := s.store.StaleDefault(p.Name); err != nil {
		trouble = append(trouble, err.Error())
	} else {
		out.stale = stale
	}
	saved, err := serve.Layouts(s.pages, s.state, p)
	if err != nil {
		trouble = append(trouble, err.Error())
	}
	out.saved = saved
	out.trouble = strings.Join(trouble, "; ")
	return out
}

// stray reports whether any saved version holds positions this graph has
// nowhere to put.
func (r row) stray() bool {
	for _, l := range r.saved {
		if len(l.Missing) > 0 {
			return true
		}
	}
	return false
}

// searchable is everything a person could reasonably expect free text to look
// in: what the file is called, what the deployment calls it, what somebody
// wrote about it, and what the saved versions are named.
func (r row) searchable() string {
	parts := []string{r.page.Rel, r.page.Name, r.entry.Title, r.entry.About,
		r.entry.Label, r.meta.Title, r.meta.Note, r.meta.CreatedBy, r.current}
	parts = append(parts, r.meta.Tags...)
	parts = append(parts, r.meta.Maintainers...)
	for _, l := range r.saved {
		parts = append(parts, l.Name)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// keeps answers whether a page survives the screening.
func (sc screen) keeps(r row) bool {
	if sc.Text != "" && !strings.Contains(r.searchable(), strings.ToLower(sc.Text)) {
		return false
	}
	// A tag is a token out of a small set somebody chose, so it is matched
	// whole. A person's name is written down in more than one form — a login
	// here, a display name there — so that one is matched by part.
	for _, want := range sc.Tags {
		if !holds(r.meta.Tags, want) {
			return false
		}
	}
	if sc.Who != "" && !mentions(r.meta, sc.Who) {
		return false
	}
	if sc.Kind != "" && !strings.EqualFold(r.entry.Kind, sc.Kind) {
		return false
	}
	switch sc.State {
	case statePromoted:
		if r.current == "" || r.stale != "" {
			return false
		}
	case statePlain:
		if r.current != "" {
			return false
		}
	case stateStale:
		if r.stale == "" {
			return false
		}
	}
	switch sc.Fit {
	case fitNone:
		if len(r.saved) > 0 {
			return false
		}
	case fitPartial:
		if !r.stray() {
			return false
		}
	case fitComplete:
		if len(r.saved) == 0 || r.stray() {
			return false
		}
	}
	return true
}

func holds(all []string, want string) bool {
	for _, at := range all {
		if strings.EqualFold(at, want) {
			return true
		}
	}
	return false
}

func mentions(m manage.Meta, who string) bool {
	who = strings.ToLower(who)
	if strings.Contains(strings.ToLower(m.CreatedBy), who) {
		return true
	}
	for _, at := range m.Maintainers {
		if strings.Contains(strings.ToLower(at), who) {
			return true
		}
	}
	return false
}

// index lists what is saved, narrowed to what somebody asked for.
func (s *site) index(w http.ResponseWriter, r *http.Request) {
	// Reading covers seeing what is saved for a diagram, and this page is a
	// list of exactly that: which pages exist, what has been saved for each,
	// and how much of it lands. /manage and /roles ask; this one did not.
	if d := s.may(r, authz.Read, ""); !d.Allowed {
		http.Error(w, d.Because, http.StatusForbidden)
		return
	}
	pages, err := serve.Pages(s.pages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// The same rule as /manage, and it is applied before any screening: a page
	// somebody may not open is not one whose saved versions they should be
	// reading the names of, and a condition that happened to exclude it anyway
	// would be doing the right thing by accident.
	rows := make([]row, 0, len(pages))
	for _, p := range pages {
		if d := s.may(r, authz.Read, p.Name); !d.Allowed {
			continue
		}
		rows = append(rows, s.gather(p))
	}

	sc := screenFrom(r.URL.Query())
	kept := make([]row, 0, len(rows))
	var unreadable []string
	for _, at := range rows {
		if at.trouble != "" {
			unreadable = append(unreadable, at.page.Rel)
		}
		if sc.keeps(at) {
			kept = append(kept, at)
		}
	}

	var b strings.Builder
	s.chrome(&b, "layouts")
	s.screenForm(&b, sc, r)

	if len(unreadable) > 0 {
		// A condition asked about something that could not be read answers
		// "no", and the page drops out of the listing looking exactly like a
		// page the person deliberately screened out.
		b.WriteString(`<p class=warn>Some of what is written down could not be read, so ` +
			`a screening on it is incomplete: ` + html.EscapeString(strings.Join(unreadable, ", ")) + `</p>`)
	}

	switch {
	case len(rows) == 0:
		b.WriteString(`<p class=m>No rendered pages under this directory.</p>`)
	case len(kept) == 0:
		b.WriteString(`<p class=m>Nothing here matches that. ` +
			fmt.Sprint(len(rows)) + ` pages are being screened out — ` +
			`<a href="/layouts">show all of them</a>.</p>`)
	case len(kept) < len(rows):
		b.WriteString(`<p class=m>` + fmt.Sprint(len(kept)) + ` of ` + fmt.Sprint(len(rows)) +
			` pages. <a href="/layouts">show all of them</a></p>`)
	}

	for _, at := range kept {
		s.writeRow(&b, at)
	}

	b.WriteString(`<p class=m>A box drawn in the browser has two facts about it: that it ` +
		`exists, which an overlay carries, and where it sits, which a layout carries. ` +
		`Saving one without the other loses the box, so both are saved together and ` +
		`opened together.</p>`)
	b.WriteString(`<p class=m>A layout applies to whatever the page carries now. ` +
		`Positions that match nothing are kept in the file and listed here rather ` +
		`than dropped, so a layout shared with a narrower view does not lose them.</p>`)
	b.WriteString(actScript + screenScript)
	s.send(w, &b)
}

// writeRow draws one page: what is attached to it, then what is saved for it.
//
// What a screening can be written about is shown, because a condition matching
// something invisible looks like the listing misbehaving.
func (s *site) writeRow(b *strings.Builder, at row) {
	b.WriteString(`<h2><a href="/` + html.EscapeString(at.page.Rel) + `">` +
		html.EscapeString(at.page.Rel) + `</a>`)
	var about []string
	if at.entry.Title != "" && at.entry.Title != at.page.Rel {
		about = append(about, at.entry.Title)
	}
	if at.entry.Label != "" {
		about = append(about, at.entry.Label)
	}
	if len(about) > 0 {
		b.WriteString(` <small>` + html.EscapeString(strings.Join(about, " · ")) + `</small>`)
	}
	b.WriteString(`</h2>`)

	var written []string
	if len(at.meta.Tags) > 0 {
		written = append(written, "tagged "+strings.Join(at.meta.Tags, ", "))
	}
	if len(at.meta.Maintainers) > 0 {
		written = append(written, "looked after by "+strings.Join(at.meta.Maintainers, ", "))
	}
	if at.meta.CreatedBy != "" {
		written = append(written, "written down by "+at.meta.CreatedBy)
	}
	if len(written) > 0 {
		b.WriteString(`<p class=m>` + html.EscapeString(strings.Join(written, " — ")) + `</p>`)
	}
	if at.trouble != "" {
		b.WriteString(`<p class=warn>` + html.EscapeString(at.trouble) + `</p>`)
	}
	if at.stale != "" {
		b.WriteString(`<p class=warn>Set to be drawn with <code>` +
			html.EscapeString(at.stale) + `</code>, which is not saved any more. ` +
			`It is being drawn as generated.</p>`)
	}

	if len(at.saved) == 0 {
		b.WriteString(`<p class=m>None saved yet. Open the page, switch to Edit, ` +
			`move things or press 整列, then Save. The page comes back to what ` +
			`you saved; 既定にする is what makes everybody else get it too.</p>`)
		return
	}
	b.WriteString(`<table><tr><th>saved<th>positions<th>placed<th>not in this graph`)
	for _, l := range at.saved {
		href := "/" + at.page.Rel + "?layout=" + url.QueryEscape(l.Name)
		missing := "—"
		if n := len(l.Missing); n > 0 {
			missing = fmt.Sprintf("%d (%s)", n, strings.Join(l.Missing[:min(3, n)], ", "))
		}
		paired := ""
		if l.Paired {
			// The box somebody drew lives in the overlay; without it the
			// layout has a position for something that does not exist.
			href += "&overlay=" + url.QueryEscape(l.Name)
			paired = ` <small>+ what it asserts</small>`
		}
		if l.Name == at.current {
			paired += ` <small>current</small>`
		}
		b.WriteString(`<tr><td><a href="` + html.EscapeString(href) + `">` +
			html.EscapeString(l.Name) + `</a>` + paired + `<td>` + fmt.Sprint(l.Nodes) +
			`<td>` + fmt.Sprint(l.Placed) + `<td>` + html.EscapeString(missing))
	}
	b.WriteString(`</table>`)
}

// screenForm draws the conditions, what this person has kept, and who this
// person is taken to be.
//
// It is a plain form that navigates, so narrowing works with no script at all
// and every screening is a url somebody can send to somebody else. Only
// keeping one needs the browser to do anything.
func (s *site) screenForm(b *strings.Builder, sc screen, r *http.Request) {
	b.WriteString(`<form class=screen method=get action="/layouts">`)
	field(b, "text", `<input type=search name=q value="`+html.EscapeString(sc.Text)+
		`" placeholder="path, note, saved name">`)
	field(b, "tags", `<input name=tag value="`+html.EscapeString(strings.Join(sc.Tags, ", "))+
		`" placeholder="all of them">`)
	field(b, "person", `<input name=who value="`+html.EscapeString(sc.Who)+
		`" placeholder="maintainer">`)
	if s.cfg != nil && s.cfg.Catalog != nil && len(s.cfg.Catalog.Kinds) > 0 {
		// Only offered where the deployment said what its groups are. An empty
		// list of kinds would be a control that cannot narrow anything.
		var opts strings.Builder
		opts.WriteString(`<select name=kind>` + option("", "any", sc.Kind))
		for _, k := range s.cfg.Catalog.Kinds {
			label := k.Label
			if label == "" {
				label = k.ID
			}
			opts.WriteString(option(k.ID, label, sc.Kind))
		}
		opts.WriteString(`</select>`)
		field(b, "kind", opts.String())
	}
	field(b, "drawn with", `<select name=state>`+
		option("", "any", sc.State)+
		option(statePromoted, "a chosen version", sc.State)+
		option(statePlain, "as generated", sc.State)+
		option(stateStale, "one that has gone", sc.State)+`</select>`)
	field(b, "positions", `<select name=fit>`+
		option("", "any", sc.Fit)+
		option(fitComplete, "all land", sc.Fit)+
		option(fitPartial, "some land nowhere", sc.Fit)+
		option(fitNone, "nothing saved", sc.Fit)+`</select>`)
	b.WriteString(`<button>narrow</button>`)
	if !sc.empty() {
		b.WriteString(`<a href="/layouts">clear</a>`)
	}
	b.WriteString(`</form>`)
	s.keptScreens(b, sc, r)
}

// keptScreens shows the screenings this person saved, and who this person is.
//
// The two belong together. A screening is filed under a name, this server asks
// nobody for one, and a list of saved conditions with no way to see or change
// whose they are is a feature that appears to lose things.
func (s *site) keptScreens(b *strings.Builder, sc screen, r *http.Request) {
	who := s.actor(r)
	kept, err := s.store.Screens(who)
	if err != nil {
		b.WriteString(`<p class=warn>What you kept could not be read: ` +
			html.EscapeString(err.Error()) + `</p>`)
	}

	b.WriteString(`<p class=kept>`)
	if len(kept) == 0 {
		b.WriteString(`<span class=m>Nothing kept yet.</span> `)
	}
	for _, k := range kept {
		// Never the stored string itself. It is parsed back into the
		// conditions this build knows and the link is rebuilt from those, so
		// what a screening can do is bounded by what this page understands
		// rather than by what was once written into a file.
		q, _ := url.ParseQuery(strings.TrimPrefix(k.Query, "?"))
		b.WriteString(`<a href="` + html.EscapeString(screenFrom(q).href()) + `">` +
			html.EscapeString(k.Name) + `</a> <button data-method="DELETE" data-to="/api/screens/` +
			html.EscapeString(url.PathEscape(k.Name)) + `">forget</button> `)
	}
	if !sc.empty() {
		// Offered only when there is something to keep. Keeping the empty
		// screening would save a link to the listing somebody is already
		// looking at.
		b.WriteString(`<input id=screen-name placeholder="name this screening" maxlength=64>` +
			`<button id=keep-screen>keep it</button>`)
	}
	b.WriteString(`</p>`)

	shown := who.Name
	if shown == "" {
		shown = manage.Anonymous
	}
	b.WriteString(`<p class=kept><span class=m>Kept for <b>` + html.EscapeString(shown) +
		`</b>, which nothing checked — this mode asks nobody who they are.</span> ` +
		`<input id=who-i-am value="` + html.EscapeString(who.Name) +
		`" placeholder="name" maxlength=64><button id=name-me>be this</button>`)
	if who.Name != "" {
		b.WriteString(` <button data-method="DELETE" data-to="/api/whoami">be nobody</button>`)
	}
	b.WriteString(`</p>`)
}

func field(b *strings.Builder, label, control string) {
	b.WriteString(`<label>` + html.EscapeString(label) + control + `</label>`)
}

func option(value, label, chosen string) string {
	selected := ""
	if value == chosen {
		selected = ` selected`
	}
	return `<option value="` + html.EscapeString(value) + `"` + selected + `>` +
		html.EscapeString(label) + `</option>`
}

// screenScript is the two buttons that send something somebody typed. The rest
// of this page is a form and links; actScript handles everything that only
// needs a url.
const screenScript = `<script>
(() => {
  const send = async (to, method, body) => {
    const res = await fetch(to, {method: method,
      headers: body ? {'Content-Type': 'application/json'} : {},
      body: body ? JSON.stringify(body) : undefined});
    const text = await res.text();
    if (res.ok) location.reload(); else alert(text);
  };
  const typed = (id) => (document.getElementById(id).value || '').trim();
  const on = (id, fn) => { const el = document.getElementById(id); if (el) el.onclick = fn; };
  on('name-me', () => {
    const name = typed('who-i-am');
    if (!name) { alert('Type a name first, or press "be nobody".'); return; }
    send('/api/whoami', 'POST', {name: name});
  });
  on('keep-screen', () => {
    const name = typed('screen-name');
    if (!name) { alert('Name it first, so it can be found again.'); return; }
    send('/api/screens', 'POST', {name: name, query: location.search});
  });
})();
</script>`
