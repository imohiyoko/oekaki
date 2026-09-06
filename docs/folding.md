# Folding: too much in one picture

Underneath every other complaint about these drawings is one complaint: a
picture carries more than a picture can carry, and what comes out is a grey mat
of boxes. An estate does not have to be large for this — a hundred boxes is
enough — and no amount of layout tuning fixes it, because the problem is the
amount of information rather than its arrangement.

The wrong answers are easy to reach for:

| | why not |
| --- | --- |
| draw less (filter) | throws away what somebody came to see |
| draw smaller | an unreadable picture, unreadable at a smaller size |
| let the reader fold it | asks them to find the thing to fold in the picture they cannot read |

So the tool folds, and says what it folded.

```console
$ oekaki render plan.json --fold -o architecture.svg
96 boxes folded to 23: 8 folds (8 chain) standing for 81
```

A fold is not a deletion. The box that stands in says how many it stands for,
the record says which ones, and nothing here decides that anything is
unimportant — that is the reader's judgement. It decides only what can be said
in fewer boxes **without saying anything different**.

## The rules, in the order they run

| | |
| --- | --- |
| `chain` | a run of boxes that pass something along with nothing else attached. The ends stay; the middle becomes one box saying how far it is |
| `twins` | boxes that are the same thing, in the same place, joined to the same things. The only thing lost is that there were four, and the count keeps it |
| `leaves` | everything hanging off one box by a single line. The shape of the drawing is untouched; the halo around one box becomes a number |

`chain` runs first even though `twins` is the cheaper fold, because where both
apply the chain is the true description. Ten services passing a request down a
line are not ten interchangeable copies: folding them as twins would say there
were ten of the same thing, which is a different claim from ten hops.

### What makes two boxes twins

Same type, same container, same provider — and also the same **coverage state**
and the same **claim origin**. A workload nothing logs and one that logs are not
the same box in a drawing whose subject is which is which, and a thing a person
asserted is not the same as a thing a parser found.

Then: joined to the same things. A pod that also writes to a database is not
interchangeable with the pods that do not, and folding it away would hide the
only interesting thing about it.

With one exception, which is what makes the rule useful at all. Four workers
that call each other have four different neighbour sets — each other's — so
comparing those means a set of peers can never be folded, and a set of peers is
the commonest crowd there is. The identity of a peer going into the same box is
not a difference between them; the **number** of peer links is, so that stays in
the signature and an odd one out stays out. What happens inside the fold is
kept as a count on the box rather than as a line from the box to itself.

## Why a budget rather than a switch

"Fold twins" is a rule. "Make this readable" is the request, and a budget is
the request: rules run until the drawing is inside it and then stop, so nothing
is folded that did not need to be, and the same settings work on an estate of
forty resources and one of four thousand.

```console
$ oekaki render plan.json --fold --fold-budget 40 -o architecture.svg
$ oekaki render plan.json --fold --fold-rules twins,leaves -o architecture.svg
$ oekaki render plan.json --fold --fold-keep aws_ecs_service.api -o architecture.svg
```

`--fold-keep` names a box that must be drawn as itself however crowded the
drawing is. Whatever the reader is looking at is not something to fold away
underneath them.

## What travels through a fold

A line that pointed at a folded box points at the box standing in its place,
carrying how many references it stands for. A route through folded boxes is a
route through what stands for them, with consecutive participants that land on
the same box collapsed — and a route with nothing left to say goes.

A reading about a box that is no longer drawn is dropped rather than rolled up.
Adding four workers' measurements together would be this package inventing a
measurement nobody took.

A claim travels only when every member agreed about who said so. A box standing
for one thing a person asserted and three a parser found is not an assertion,
and drawing it as one would put somebody's name on three things they never
said.

## Opt-in, for now

`--fold` is a flag rather than the default, because turning it on would rewrite
every diagram anybody has committed. The intent is for it to become the default
once it has been used on enough real estates to trust the rules and the budget;
the number in `DefaultFoldBudget` is a starting point taken from the diagrams in
this repository, not a measured threshold.

## Unfolding in place

In the HTML viewer a fold opens where it stands: a stacked box takes a double
click, or the button in the detail panel, and the boxes it stood for come back.

That is why the record exists. The page carries the *unfolded* graph and the
list of what was folded, and folds it itself — so opening one is a matter of
not folding it, and nothing has to be fetched or regenerated. The rules still
run once, in Go: the browser is a switch over a list rather than a second
implementation of them, which is what keeps the page and the committed SVG the
same drawing.

Every other format gets the folded graph, because a picture cannot be opened.
Both draw the same thing: the page merges the lines that land on the same pair
the way the projection does, so twenty lines to a box standing for twenty are
one line carrying twenty, in the page as in the SVG.

### With an atlas

An atlas is a bound set of pages — a level, an element's detail, a call chain —
and **every** one of them is folded on its own terms.

```console
$ oekaki render plan.json --atlas --fold --fold-budget 20 -o estate.html
16 folds (16 twins) standing for 296 boxes, on 7 of 148 pages
```

Per page, because a crowd on one is not a crowd on another: a workload is on
its level and on its own detail page, and one budget spent against the whole
estate would land on a page holding three of a fold's twelve members and still
say twelve. Every page inside its budget is left alone, so a crowded level does
not cost the quiet ones their detail.

The two answer different halves of the same problem, and they compose: the
atlas decides how much of the estate a page is about, and folding decides how
much of that page is drawn as itself. Opening a namespace of forty replicas
gives three boxes — the service, the replicas, and their config — and the
replicas open where they stand.

Openings are untouched. A box that is folded away is not drawn, so nothing asks
whether it opens anything; when the reader puts the fold back, the way down
comes back with it.

An atlas is a thing an interactive page has. Asked for with any other format,
it has no pages to be, so folding falls back to folding the estate as a whole
— which is what `--fold` alone does — and the run says the atlas was ignored.
A flag that quietly does nothing is the one outcome worth avoiding here: a
reader who asked for a readable drawing and got the mat of boxes has no way to
tell that the combination was the reason.
