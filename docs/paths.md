# Paths: what actually gets walked

Everything else in this graph is said one hop at a time. An edge records that
checkout calls the ledger. Nothing records that a request arrives at the
gateway, passes through checkout and ends at the ledger — and that is the thing
an operator acts on.

A **path** is that order, as an entity: an ordered list of participants with a
claim, carrying the same three kinds an edge does.

| kind | means |
| --- | --- |
| `iac_ref` | the configuration says this route exists |
| `reachable` | the network permits it |
| `observed` | something walked it |

The gap between the first and the last is the point, exactly as it is for
edges. One level up, it answers questions an edge cannot.

```console
$ oekaki graph plan.json --traces spans.json -o graph.json
$ oekaki paths graph.json --since 30d
```

```
partial     gateway → reports → archive
            walked as far as reports; nothing has been seen going on to archive  (last 2026-05-01T10:00:00Z, 1 requests)
unexpected  gateway → ledger
            something walked this route and no declared route contains it in this order  (last 2026-09-03T02:13:00Z, 1 requests)
```

## The four answers

| | |
| --- | --- |
| `unused` | declared, and nothing has ever been seen walking it — a route to delete |
| `partial` | walked as far as some hop and never further — requests reach the ledger and never go on to the archive |
| `quiet` | walked, and then stopped, before `--since` |
| `unexpected` | walked, and no declared route contains it *in that order* |

They are not severities. Which of them matters is a property of the estate: an
unused route in a service being retired is the goal, and a route that fired
unannounced in a payment system is an incident. The listing names what was
found and leaves ranking to the reader.

### Why `partial` exists

Without it, a request that stopped early has to be either a walk of the
declared route or a surprise, and both are wrong. Reporting it as unannounced
is the false alarm that makes a listing worth ignoring; reporting the declared
route as unused is a false negative in the other direction. Which hop it stops
at is the useful part.

### Why comparison is by run and not by set

The order is the whole reason a path is an entity. A request that went gateway
then ledger is not a walk of gateway, checkout, ledger with a hop missing: it
is a different thing happening, and it is the one worth waking somebody for. So
an observed route counts as declared only when some declared route contains it
as **consecutive** participants.

## Where the two sides come from

**Observed** routes come from traces. `collectors/traces` folds spans into the
routes they walked: a trace is a tree, so each root-to-leaf chain is one route,
because flattening a fan-out into a single walk would claim an order between
two branches that nothing observed. A route is rooted at a span that names no
caller — a trace whose entry span was sampled away is skipped rather than
rooted at whichever service sorted first.

The same route in a thousand traces is one path carrying a count of a thousand.
Traffic moves the number, not the size of the document.

**Declared** routes are best written down — in an overlay, from an API
definition, from a routing table. Until something does, `oekaki paths` derives
them by following declared references from wherever a request can arrive, and
says so on stderr rather than presenting them as somebody's claim. A route is
only as declared as its weakest hop: one that depends on a rule the network
merely permits is `reachable`, not `iac_ref`.

## How a count becomes an alert

The number of walks is an ordinary observation whose subject is the path's key:

```json
{ "subject": "path:Z2F0ZXdheQ.Y2hlY2tvdXQ.bGVkZ2Vy", "metric": "path_requests",
  "value": 1284, "observed_at": "2026-09-02T10:00:01Z" }
```

That is deliberate. Everything that already knows how to read a measurement —
a threshold, a window, the viewer's time cutoff, two collectors disagreeing —
knows how to read this one, and no second mechanism had to be built for
routes. A collector holding Datadog or Prometheus credentials writes the same
document `collectors/datadog` already writes; only the subject changes.

`enrichers/observations` turns a threshold into a state, which is the spike.
The silence is the one a threshold reads wrong — the interesting value is zero,
and `value > limit` calls that healthy — so it is the `quiet` finding above
rather than a bound.

## What is deliberately not read

Nothing here looks at request or response bodies. A route is who, in what
order, how often, and when last, which is everything the four findings need,
and none of it is customer data. The IR has no field that would hold a body,
and `LogRecordSummary` exists so that a graph can be shared without carrying
one.

If bodies are ever worth capturing, the rule that keeps that safe is that they
do not enter the graph document: they stay where the collector put them, behind
that store's own access control, and the graph carries a reference. Visibility
is then a question for [`authz`](../authz), whose permission catalog is fixed
in code — a fourth permission below `read` is where it would go, denied unless
a role says otherwise.

## Determinism

`views.Paths` reads no clock. `--since 30d` is resolved by the command, against
the caller's clock, and the resolved moment is written into the JSON output —
so a listing says which moment it was asking about even when the question was
relative. Two runs over the same document produce the same list, which is what
makes a finding something you can commit, diff, and argue with.
