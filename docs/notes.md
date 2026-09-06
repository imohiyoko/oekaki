# Notes: what people know that no file says

A drawing is derived from files, and the files say what the estate is, never
what happened to it. "This one times out from September", "401 goes back
upstream, 502 is retried once", "do not scale this below two, we found out the
hard way" — none of that is in a plan file, and all of it is what somebody
actually needs when they open the picture at three in the morning.

So a node, a group and a line can each be written about, in Markdown, by name.

## Reading them

They appear in the panel beside the thing, under **メモ**, with who wrote each
one. There can be several — two people writing about the same service is the
ordinary case, and nothing here decides that the newer sentence replaced the
older one.

## Writing one

The pen is at the top right of the panel:

```
┌─ メモ ────────────────────── ✎ ─┐
│ 決済の入り口。リトライは 3 回まで。 │
│   human · operator               │
└──────────────────────────────────┘
```

It is in the panel rather than on the box, for the same reason everything else
about a thing is: a box is a few pixels tall on a drawing scaled to fit, and a
note is read next to the rest of what is known about the thing.

Writing is asserting. What you type becomes a pending assertion like every
other edit in the viewer — it is not written into the graph the page was given
— and it leaves as an overlay document through the same export button:

```json
{
  "kind": "oekaki.overlay",
  "version": "0.1",
  "metadata": { "origin": "human", "author": "operator" },
  "assertions": [
    {
      "assert": "note",
      "subject": { "node": "aws_ecs_service.checkout" },
      "text": "**決済の入り口。** リトライは `3` 回まで。"
    }
  ]
}
```

Which is also how a note is written without the viewer at all: an overlay in a
repository, reviewed like anything else, applied on every render.

```console
$ oekaki graph plan.json --overlay notes.json -o graph.json
```

The subject is resolved by the same selectors as every other assertion, so
`{"name": "checkout", "type": "aws_ecs_service"}` works, and a selector that
names two things writes about neither and says so — guessing which one was
meant is worse than saying nothing.

Because export is how a note leaves the page, the export control is offered
while reading and not only while editing. A note is written from the panel by
somebody who came to read.

## What the formatting is

Paragraphs, `**bold**`, `*italic*`, `` `code` `` and `-` bullets. That is the
whole list, and the shortness of it is the point: every addition is another
shape somebody else's text can take inside a page that draws your estate.

Links are absent on purpose. A link is a destination, and a destination in a
note is a place this page would send a reader on the say-so of whoever wrote
the note.

## Why it is safe

A note is somebody else's text arriving in a document that gets drawn. The
viewer builds elements and sets `textContent`; there is no string being
assembled anywhere in that path, so there is nothing that could be read back as
markup. A note reading

```
<img src=x onerror="…"> and <script>…</script> and [a link](javascript:…)
```

is displayed as those characters. No element is created from it and nothing in
it runs — which is checked in the browser, not only asserted here.

## Why it is not a field on the node

Claims in this format are things with an author. A string on the resource would
be the graph saying so itself, when the whole point is that a person said it,
and which person. Kept beside the graph with a subject and a claim, a note sits
exactly where an observation sits: evidence about a subject rather than a
property of it.

The shape is in [schema.md](schema.md#notes).

## What is not here yet

**Permissions of their own.** A served page is already gated: `read` on the
diagram is `read` on everything in it, notes included (see [authz](../authz)).
What is not answered is anything finer — a note only some readers should see,
or a reader who may read every note and write none. A static page cannot answer
it at all, because it has no idea who opened it.

The shape is ready for the answer: a note is already signed, already separable
from the graph, and already arrives as its own document, so a server that knows
who is asking can filter `notes` on the way out and refuse an assertion on the
way in without any of this changing. See [roadmap.md](roadmap.md).

**Editing and deleting.** A note is added; nothing supersedes one yet. That is
the same question as every other conflicting claim in this format, and it is
answered the same way — record both, rank by origin — rather than by letting
the second writer erase the first.
