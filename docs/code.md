# The code graph

`--source-dir` (and `--repo`) read a source tree and produce a graph in the
same IR everything else here uses. It is deliberately a *conservative* reading:
one that would rather record less than record something nobody wrote.

```console
$ oekaki graph src --source-dir src -o code.json
```

## What is in it

| node | what it is |
| --- | --- |
| `code_file` | one file |
| `code_package` | something imported, whether or not it is in this tree |
| `code_function` | a function or a method |
| `code_type` | a class, struct, interface, enum, trait or alias |

| relation | between | what it says |
| --- | --- | --- |
| `contains` | file → function, file → type | it was declared in that file |
| `imports` | file → package | the file imports it |
| `calls` | function → function | the caller names the callee |
| `declares` | type → function | the function is a method on that type |
| `extends` | type → type | a base the declaration named |
| `implements` | type → type | an explicit `implements` clause |
| `embeds` | type → type | Go embedding: a field or interface element with no name |
| `has_field` | type → type | a field whose type is another type here; the field names are on the edge |

## Why a method is not a second thing

A method is one function, seen from the type's side. `declares` points at the
function node the file already contains, so a method appears exactly once in
the graph and the two ways of arriving at it — down through the file, or across
from the type — reach the same box.

## What a relation needs before it is drawn

**The other end has to be a type this parser read.** A base class from a
library nobody handed us is a name, and a box drawn from a name is a box nobody
can open. Names are resolved against the types declared in the same directory
first — a directory is one package in Go and one module's worth of code nearly
everywhere else — and then against the whole tree, but only when exactly one
type carries that name. Two `Order`s in two packages is the ordinary shape of a
repository, and choosing one of them would draw an arrow nobody meant.

**Nothing is inferred from method sets.** A type is not recorded as
implementing an interface because it happens to have the right methods. That is
a type checker's work, it is wrong more often than not without one, and a wrong
arrow in a design diagram is worse than a missing one.

**The colon form cannot tell a base from an interface.** Kotlin, Swift, Scala
and C# write `class A : B` whether `B` is a class or an interface, so this
records `extends` rather than choosing. Java and TypeScript spell `implements`
separately, and there it is recorded as what it says.

## Go reads its own declarations properly

Go files go through the standard library's parser rather than the regular
expressions the other languages get, and the difference shows in what can be
said:

- a struct's fields are known by name and by type, so `has_field` carries the
  field names;
- embedding is distinguishable from an ordinary field, so `embeds` means
  embedding rather than "a field that looked like one";
- `type X = Y` is recorded as an alias, which is not a new thing at all;
- a method declared in another file of the same package still reaches its type,
  because the joining happens after every file has been read.

`[]*Order` and `Order` are the same type as far as "this holds one of those"
goes — how many there are is a property of the field, and the field is not what
is being drawn. A `map` is deliberately not followed: its key and its value are
two types, and picking one would say something the declaration does not.

## The languages without a parser of their own

Everything else is read from the declaration line with regular expressions,
after comments and string literals have been masked out. A type body is scoped
by braces or by indentation, the same way a function body already was, and a
function declared inside that scope is a method on the type.

A type declared inside another type is not followed. It is rare enough that
reading it wrong is worse than not reading it.

## Adding a real parser

`source.Register(".swift", parser)` replaces the regular expressions for one
extension with something that knows the language. A registered parser is handed
the graph and emits whatever it likes, including `code_type` nodes and the
relations above; the name resolution afterwards works on what it left in the
graph, so it does not have to know this package's internals to benefit from it.

## What is not here

**Object diagrams.** An object is an instance at run time, and nothing in a
source tree observes one. Deriving instances from types would be invention of
exactly the kind this project exists to avoid — so there is no object diagram,
and there will not be one until something actually watches a program run.

**Cross-module call resolution.** Calls are recovered within a file, and across
files where an import names the target (see `addCrossFileCalls`). A call
through an interface, a callback or a container is not recovered, and is not
guessed at.
