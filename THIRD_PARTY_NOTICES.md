# Third-party notices

oekaki is licensed under Apache-2.0 (see [LICENSE](LICENSE)). The
distributed binaries statically include the components below.

The first entry is the one that matters most: **Graphviz itself is compiled
into every oekaki binary.** That is what makes `oekaki render` work
without a `dot` on your `PATH`, and it means every binary distributes
EPL-2.0-licensed code.

---

## Graphviz — Eclipse Public License 2.0

- Version 12.1.2, compiled to WebAssembly and embedded via
  [github.com/goccy/go-graphviz](https://github.com/goccy/go-graphviz)
- Upstream: <https://gitlab.com/graphviz/graphviz>
- License: <https://gitlab.com/graphviz/graphviz/-/blob/main/LICENSE>

The EPL-2.0 is a file-level copyleft. Graphviz is used unmodified and is not
statically combined into oekaki's own source; it is a self-contained
WebAssembly module executed at runtime. Source for the covered work is
available at the upstream URL above.

## maxGraph — Apache License 2.0

- Version 0.24.0, bundled to `renderers/html/vendor/maxgraph.bundled.js`
- Upstream: <https://github.com/maxGraph/maxGraph>
- License: <https://github.com/maxGraph/maxGraph/blob/main/LICENSE>

The viewer's canvas — the boxes, the lines, and every gesture that touches
them — is maxGraph. Every page `oekaki render -f html` produces carries it:
a self-contained page inlines the bundle, and `--external-assets` writes it
beside the page as `oekaki.maxgraph.js` for the page to load.

Unlike the two entries around it, this file is **not the upstream artifact
unmodified**: it is a bundle built from the library's ES modules with esbuild,
which drops the parts the viewer does not import. `renderers/html/vendor/maxgraph.entry.js`
is the list of what it does import, `make vendor-maxgraph` rebuilds the bundle
from it, and both the library and esbuild versions are pinned in the Makefile
so the committed bytes can be reproduced. No maxGraph source is edited.

Apache-2.0 is oekaki's own licence, so this adds no term the project does
not already carry.

## Eclipse Layout Kernel (elkjs) — Eclipse Public License 2.0

- Version 0.9.3, vendored unmodified at `renderers/html/vendor/elk.bundled.js`
- Upstream: <https://github.com/kieler/elkjs>
- License: <https://github.com/kieler/elkjs/blob/master/LICENSE.md>

Every page `oekaki render -f html` produces embeds this file, so an HTML
diagram distributes EPL-2.0-licensed code — the same situation as the Graphviz
module above, and handled the same way. elkjs is used unmodified and is not
combined into oekaki's own source; it is a self-contained script the page
loads.

elkjs offers GPL-3.0-or-later as a secondary license under the EPL's Exhibit A.
**oekaki takes it under the EPL-2.0, not under the secondary license.**

It is vendored rather than fetched at build time on purpose. The project's
identity is that it cross-compiles like any pure Go program and installs with
`go install`; a build-time download would break offline builds, break
reproducibility, and break `go install ...@version` outright.

## Go modules

| Module | License |
| --- | --- |
| [github.com/goccy/go-graphviz](https://github.com/goccy/go-graphviz) | MIT |
| [github.com/tetratelabs/wazero](https://github.com/tetratelabs/wazero) | Apache-2.0 |
| [github.com/hashicorp/terraform-json](https://github.com/hashicorp/terraform-json) | MPL-2.0 |
| [github.com/hashicorp/go-version](https://github.com/hashicorp/go-version) | MPL-2.0 |
| [github.com/zclconf/go-cty](https://github.com/zclconf/go-cty) | MIT |
| [github.com/santhosh-tekuri/jsonschema/v5](https://github.com/santhosh-tekuri/jsonschema) | Apache-2.0 |
| [github.com/apparentlymart/go-textseg/v15](https://github.com/apparentlymart/go-textseg) | MIT, with Apache-2.0 generator code and Unicode-DFS data |
| [github.com/fogleman/gg](https://github.com/fogleman/gg) | MIT |
| [github.com/disintegration/imaging](https://github.com/disintegration/imaging) | MIT |
| [github.com/flopp/go-findfont](https://github.com/flopp/go-findfont) | MIT |
| [github.com/golang/freetype](https://github.com/golang/freetype) | FreeType License or GPL-2.0, at the recipient's choice |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) | BSD-3-Clause |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text) | BSD-3-Clause |

### Mozilla Public License 2.0 components

`terraform-json` and `go-version` are MPL-2.0. Source for those files is
available from their repositories above; they are used unmodified.

### FreeType

`github.com/golang/freetype` is offered under a choice of two licenses. This
project takes it under **the FreeType License**, not the GPL. That license asks
for credit:

> Portions of this software are copyright © The FreeType Project
> (<https://www.freetype.org>). All rights reserved.

It reaches the binary through go-graphviz's raster backend. oekaki does not
emit raster images — see [docs/architecture.md](docs/architecture.md) for why —
but the code is linked in regardless, so the credit is given here.

### Unicode data

`go-textseg` embeds data from the Unicode Character Database, © 1991–2016
Unicode, Inc., under the Unicode Data Files and Software License.

---

## Regenerating this file

There is no tool behind it; it was compiled by reading the license file of each
module in `go list -deps`. If you add or remove a dependency, update this file
in the same commit. A dependency whose license is not listed here is a bug.
