# The code graph

A directory of source given as the input — as the argument, or with `--repo` —
becomes a graph in the same IR everything else here uses. It is deliberately a
*conservative* reading: one that would rather record less than record something
nobody wrote.

```console
$ oekaki graph src -o code.json
$ oekaki graph --repo ../checkout --repo ../payments -o estate.json
```

(`--source-dir` is a different flag, and not this one: it points at a directory
of `.tf` files so a Terraform graph can say which file and line declared each
resource.)

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

One relation about this code is not in that table and never will be: `serves`,
which says a function answers an API operation. It is not there because nothing
here can read it — a file does not say which operation it is the other end of,
and the document that declares the operation does not say who implements it. It
arrives as a claim with an author. See [api.md](api.md#which-function-serves-it).

## Why a method is not a second thing

A method is one function, seen from the type's side. `declares` points at the
function node the file already contains, so a method appears exactly once in
the graph and the two ways of arriving at it — down through the file, or across
from the type — reach the same box.

Its name carries the type that declared it — `Order.total`, the way a Go method
always has — because a file with two classes that both declare `run` has two
methods. One node named `run` would make them one: every class in the file
declaring the same box, and a member click landing on somebody else's page. The
class diagram shows the short name, since inside the class the receiver is the
box it is written in.

## What a relation needs before it is drawn

**The other end has to be a type this parser read.** A base class from a
library nobody handed us is a name, and a box drawn from a name is a box nobody
can open. Names are resolved against the types declared in the same directory
first — a directory is one package in Go and one module's worth of code nearly
everywhere else — and then against the whole tree, but only when exactly one
type carries that name. Two `Order`s in two packages is the ordinary shape of a
repository, and choosing one of them would draw an arrow nobody meant.

**A qualified name in a type is not resolved at all.** `*http.Client`,
`models.User` and `Outer::Inner` name another package or another scope, and
this parser has no notion of which is which. Dropping the qualifier and
matching what is left would join a field to whatever local type happened to be
called `Client`, or a class to the `Outer` its base was only reached through —
an arrow to something the declaration never mentioned, in a document whose
whole purpose is telling apart what was claimed from what was seen.

**A qualified Go call is resolved, but only into this tree.** `store.Save(…)`
is drawn when the file imports a path that names the directory the target is
in, and the qualifier is what this file calls that package by — its alias, or
the package's own clause. It matters because a call chain that stops at every
package boundary is not a chain, and a service's own flow goes through those
boundaries.

The import path is what gets matched, not the last element of it, so the module
path is read from `go.mod`. Without that there is no way to turn an import path
into a directory, and a tree that declares no module keeps its calls inside a
package: matching on the last element alone would draw `import "net/http"` at a
local package called `http`, and `github.com/somebody/else/store` at this
tree's `store`.

The name is read from the package rather than guessed from the path, so
`.../go-store/v2` imported as `store` resolves, and an alias
(`import st ".../store"`) resolves under the name the file gave it.

Everything outside stays unread. `http.Get` names a package nobody handed us.
Two packages of one name are told apart by the path, because the path is what
is matched. A qualifier that is a value rather than a package — `p.Effect(…)`,
a method on a receiver — matches no import and resolves inside its own package,
the way it always has.

What stands between the package and the call decides which kind of declaration
was reached. `store.Save(1)` is the package's function and never its method;
`store.Default.Save(1)` is a method on a package variable and never the
function `store.Save`. Both are written down in the declaration, so the two are
told apart rather than guessed between.

**A local name that shadows an imported package is read as the package.**
`func Handle(store *Thing) { store.Save(1) }` in a file that also imports
`.../store` is read as a call into that package: it is drawn there if the
package declares a `Save`, and drawn nowhere at all if it does not — the method
on the parameter is not found either way. Telling the two apart means knowing
what is in scope at that line, which is a type checker's job and not this
reading's. A blank or dot import is the exception, because it binds no name:
`_ ".../store"` shadows nothing, and a `store` in that file is whatever the
file declared. It is the one place here where a name that looks right is taken at
face value, and it is written down rather than hidden.

**A declaration has to look like one.** `struct sockaddr_in addr;` declares a
variable, not a type: a name followed by another name is never a declaration,
and reading it as one produced a box for something the file never wrote. An
anonymous class (`export default class extends Base`) declares nothing this can
name, so it declares nothing here.

**Type parameters are not bases.** `class Box<T extends Number>` bounds a
parameter and descends from nothing, and `implements Map<String, Integer>`
implements one interface rather than two. The angle brackets come off before
the bases are read.

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

A member is a function the type declares, and what stops one from being a
member is something around it that owns what it declares: another function, or
a type written inline where a value goes. A `def` inside an `if
TYPE_CHECKING:`, a Ruby `class << self`, a Kotlin `companion object` is still
the class's — those braces and colons only group. A `def` inside a `def`, or a
method on a Java anonymous class, belongs to what encloses it.

A declaration that wraps is still being written. Its brace, its bases, and the
rest of a list it stopped in the middle of are on the lines that follow, and the
type stays open until one of them finishes it. A declaration that finishes
without a body — Kotlin's `class Marker`, a Rust unit struct, a C forward
declaration — is over where it started, as is a body that opens and closes on
one line, and what comes next belongs to the file, not to the type.

A call written inside a type means that type's method when it has one of that
name, in the languages where a method can be called that way. Python,
JavaScript and PHP are not among them: a bare `render()` there is the module's
function, and the method is `self.render()`, `this.render()` or
`$this->render()`. A call written on anything else — `other.render()` — is a
method of whatever that name holds, which is not something this reading can
know, and it is not the module's function either, because the bare name would
have been written for that. It names nothing. Outside a type, a call means the
function of that name before anybody's method. Two classes in one file with a
`paint` each call their own.

What is written between a brace and its closing brace on the same line goes
unread, because every function pattern here is anchored to the start of a line.
That is a limit of reading code with regular expressions rather than of the
scope tracking, and it is what `source.Register` is for.

## Adding a real parser

`source.Register(".swift", parser)` replaces the regular expressions for one
extension with something that knows the language. A registered parser is handed
the graph and emits whatever it likes, including `code_type` nodes and the
relations above; the name resolution afterwards works on what it left in the
graph, so it does not have to know this package's internals to benefit from it.

## Clicking a type

In an atlas (`--atlas`), a type opens as a **class diagram** rather than as the
generic "what is inside this box" page. It is the same page — one element has
one inside — read the way the thing itself is written:

- the type in the middle, with what it declares listed inside the box, under a
  rule, the way UML puts members in a compartment. A class with nine methods
  drawn as nine boxes is a picture of nine things, when it is a picture of one
  thing with nine methods;
- the second line says which kind of type it is, because a class diagram that
  draws an interface and a struct the same way has lost what it was drawn for;
- every other type the declaration mentions, on the relation that mentions it,
  and each of those boxes opens as its own class diagram. That is how a reader
  walks a design.

Nothing is lost by listing the members instead of drawing them: the file that
contains the type contains its functions too, and that page still draws every
one of them as a box a reader can open — with its calls, and a sequence where
there is a chain to make one from.

The class page keeps types and members and nothing else, so a function that is
merely *near* a type — a constructor, something that takes one as an argument —
is not on it. That is the page saying what a class diagram says: this is the
thing, this is what it declares, these are the other things it mentions. Who
uses it is a different question, and the file's page and the function's own
page are where it is answered.

A type with nothing to say — no members, and no relation to another type — has
no class page at all, and no box offers a door into one. A door into an empty
room is worse than no door: a reader who opens two of them stops trying the
third.

A type with more members than fit says how many it left out, rather than
growing into a box the length of the page.

The **package diagram** is the level hierarchy an atlas already builds:
reading a source tree puts every file in the group of the directory it is in,
so the levels of an atlas are the directories, and descending one is descending
into a package.

## What is not here

**Object diagrams.** An object is an instance at run time, and nothing in a
source tree observes one. Deriving instances from types would be invention of
exactly the kind this project exists to avoid — so there is no object diagram,
and there will not be one until something actually watches a program run.

**Cross-module call resolution.** Calls are recovered within a file, and across
files where an import names the target (see `addCrossFileCalls`). A call
through an interface, a callback or a container is not recovered, and is not
guessed at.
