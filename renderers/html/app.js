/* The interactive view.
 *
 * Everything drawn here comes out of the embedded IR. Nothing is recomputed
 * from appearance: the coverage state is a string in the document, so
 * filtering is `n.coverage.state === 'blind'` rather than an inspection of
 * colours. That is why the state was made a field rather than a rendering
 * decision.
 */
(() => {
  'use strict';

  // A page carries either one diagram or a bound set of them. The atlas is the
  // set; `graph` is whichever of its pages is on screen. Everything below this
  // reads one graph, and navigating swaps that graph and rebinds the indexes
  // rather than teaching several hundred lines about pages.
  //
  // A set that cannot be read is not a reason to show nothing. The renderer
  // promises that a page falls back to the drawing it has always produced,
  // and that promise is only worth what this parse does: an atlas that is
  // malformed, or whose root is not among its own diagrams, is treated as no
  // atlas at all rather than as an empty one — an empty one would take the
  // atlas path through every function below and leave the reader with a
  // breadcrumb bar containing nothing and no way back.
  const atlasElement = document.getElementById('oekaki-atlas');
  let atlas = null;
  let atlasBroken = '';
  if (atlasElement && atlasElement.textContent.trim()) {
    try {
      atlas = JSON.parse(atlasElement.textContent);
    } catch (err) {
      atlasBroken = 'the set of diagrams in this page could not be read; showing the graph on its own';
    }
  }
  const pages = new Map(((atlas && atlas.diagrams) || []).map((d) => [d.id, d]));
  if (atlas && !pages.has(atlas.root)) {
    atlasBroken = 'this page names a diagram it does not carry; showing the graph on its own';
    atlas = null;
  }
  let page = atlas ? pages.get(atlas.root) : null;
  let graph = page ? page.graph : JSON.parse(document.getElementById('oekaki-graph').textContent);
  // Where a click leads, by element id. It is recorded by the derivation
  // rather than worked out here, because whether a box has an inside is a
  // property of what was drawn from, and a door into an empty room is worse
  // than no door at all.
  let opens = new Map();
  const layoutElement = document.getElementById('oekaki-layout');
  const savedLayout = layoutElement && layoutElement.textContent.trim()
    ? JSON.parse(layoutElement.textContent) : null;
  const positions = new Map((savedLayout && savedLayout.nodes || []).map((n) => [n.id, n]));
  // A height chosen by hand. Kept apart from the position because a box can be
  // resized without being placed, and placed without being resized.
  const sizes = new Map((savedLayout && savedLayout.nodes || [])
    .filter((n) => n.width || n.height)
    .map((n) => [n.id, {width: n.width, height: n.height}]));
  const axis = document.body.dataset.axis || '';
  const rankdir = document.body.dataset.rankdir || 'LR';
  const layoutDirection = rankdir === 'TB' ? 'DOWN' : 'RIGHT';
  const requestedKinds = new Set((document.body.dataset.kinds || '').split(',').filter(Boolean));

  /* ---- palettes ------------------------------------------------------- */
  /* Mirrors renderers/style. Duplicated rather than generated because the two
     change at different rates and a generator would be more machinery than
     the twenty lines it saved. */

  const CATEGORY = {
    network:  {fill: '#e8f0fe', stroke: '#3b6fd4', text: '#16305e'},
    compute:  {fill: '#e7f5ec', stroke: '#3f9159', text: '#1c4429'},
    database: {fill: '#f0eafc', stroke: '#7a52c7', text: '#38215e'},
    security: {fill: '#fdf0e3', stroke: '#c97b1e', text: '#5e3a0c'},
    storage:  {fill: '#fdeef0', stroke: '#c74f63', text: '#5e1f2b'},
    generic:  {fill: '#f2f3f5', stroke: '#8a9099', text: '#33383d'},
  };

  const COVERAGE = {
    blind:      {stroke: '#c74f63', dashed: true,  width: 2.4, badge: 'no logs',    label: 'no log destination'},
    silent:     {stroke: '#c97b1e', dashed: false, width: 2.4, badge: 'silent',     label: 'declared, nothing seen'},
    undeclared: {stroke: '#7a52c7', dashed: true,  width: 2.4, badge: 'unmodelled', label: 'logs from nothing declared'},
    flowing:    {stroke: '',        dashed: false, width: 0,   badge: '',           label: 'logs flowing'},
    unknown:    {stroke: '#8a9099', dashed: true,  width: 0,   badge: '?',          label: 'not assessed'},
  };
  const COVERAGE_ORDER = ['blind', 'silent', 'undeclared', 'flowing', 'unknown'];

  const EDGE = {
    iac_ref:   {color: '#8a9099', dash: '',      width: 1.1},
    reachable: {color: '#c97b1e', dash: '5 3',   width: 1.1},
    observed:  {color: '#3b6fd4', dash: '',      width: 2.0},
  };
  const SUPPRESSED = {color: '#c0c5cb', dash: '2 3', width: 1.1};

  const GROUP = {
    vpc:            {fill: '#fbfcfe', stroke: '#5b7fa6', text: '#33506b'},
    subnet:         {fill: '#f6f8fa', stroke: '#9aa7b4', text: '#4a5763'},
    provider:       {fill: '#fcfbfe', stroke: '#8a7fb0', text: '#4a4163'},
    module:         {fill: '#fbfdfb', stroke: '#7fa68a', text: '#3f6b4f'},
    namespace:      {fill: '#f8fafd', stroke: '#7f8fb0', text: '#414f6b'},
    resource_group: {fill: '#fdf9fb', stroke: '#a67f93', text: '#6b3f52'},
  };
  const groupStyle = (t) => GROUP[t] || {fill: '#fafbfc', stroke: '#a8b0b8', text: '#4a5763'};

  /* ---- the graph, indexed --------------------------------------------- */

  // Rebound by bindGraph whenever the page turns. They are indexes of one
  // graph, and the graph is no longer fixed for the life of the document.
  let nodes = new Map();
  let groups = new Map();
  // Target text is not a namespace: an entity id may equal an encoded edge
  // key. Keep the discriminator when deciding which nodes are contested so an
  // edge disagreement cannot decorate or populate the details of that entity.
  let entityConflicts = [];
  let edgeConflicts = [];
  // The IR names an edge by base64url-encoding each part, so an id that
  // contains the separator cannot spell another edge's name. A conflict about
  // a line is recorded under that name, and so is the line on this canvas.
  const b64url = (t) => btoa(String.fromCharCode(...new TextEncoder().encode(t)))
    .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  const edgeKey = (e) => 'edge:' + [e.from, e.to, e.kind, e.relation || ''].map(b64url).join('.');

  // Which side of a box a line was told to leave and arrive on. Only the side:
  // where along it the line lands is worked out from the drawing, because
  // lines that share a side are spread along it and a number written down for
  // one of them would be wrong the moment another arrived.
  //
  // The value carries the line's own name as well as the sides, so a side
  // survives being exported while a filter has that line out of the picture.
  const edgeAnchors = new Map();
  for (const e of (savedLayout && savedLayout.edges) || []) {
    if (!e.source && !e.target && !e.line) continue;
    edgeAnchors.set(edgeKey(e), {
      from: e.from, to: e.to, kind: e.kind, relation: e.relation || '',
      source: e.source, target: e.target, line: e.line,
    });
  }

  // A curve or right angles. The diagram has one answer — it is what ELK is
  // asked for, so every line follows it, not only the ones drawn here — and a
  // single line may say otherwise.
  // The page is generated with a shape (--lines); a layout document overrides
  // it, because that one was authored and this one is only the default.
  let lineShape = ((savedLayout && savedLayout.lines) || document.body.dataset.lines) === 'orthogonal'
    ? 'orthogonal' : 'curved';
  const lineShapeOf = (key) => (edgeAnchors.get(key) || {}).line || lineShape;
  let contestedEntities = new Set();

  const state = (n) => (n.coverage ? n.coverage.state : null);
  const shortType = (t) => t.replace(/^[a-z0-9]+_/, '');

  const childGroups = (parent) =>
    [...groups.values()].filter((g) => (g.parent || null) === (parent || null)).sort(byId);
  const byId = (a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

  const groupPath = (id) => {
    const parts = [];
    for (let cur = id; cur; ) {
      const g = groups.get(cur);
      if (!g) break;
      parts.unshift(g.id);
      cur = g.parent || null;
    }
    return parts.join('/');
  };
  // Assertions made in this session are drawn, but they are not written into
  // `graph`: that object is what the input said, and the export is what makes
  // a claim real. So a box added here joins the drawing through allNodes and a
  // new label through nameOf, and neither edits what was found.
  const allNodes = () => graph.nodes.concat(pending.map((p) => p.node).filter(Boolean));
  const nodesIn = (path) => allNodes().filter((n) => ((n.groups || {})[axis] || '') === path).sort(byId);
  const renamed = new Map();
  const nameOf = (n) => renamed.get(n.id) || n.name;

  /* ---- view state ------------------------------------------------------ */

  const collapsed = new Set();
  const hidden = new Set();          // coverage states switched off
  const hiddenLabels = new Set();    // classified log labels switched off
  const pending = [];                // assertions made in this session
  // Editing is a mode, not a modifier. Reading a diagram and authoring one
  // want the same gestures for different things, and a modifier key answers
  // "what does this drag mean" only for the hand that is already holding it.
  let editing = new URLSearchParams(location.search).get('edit') === '1';
  // Which boxes are picked up. The panel is about the last one, but a drag
  // carries all of them: maxGraph moves everything it has selected, so the
  // only thing missing was a way to select more than one.
  const picked = new Set();
  let selected = null;
  let selectedGroup = null;
  let selectedEdge = null;
  let observationCutoff = 0;
  let focus = null;
  let focusNodes = null;
  const labelsBySource = new Map();

  // Everything indexed from the graph, rebuilt for whichever page is on
  // screen. One function rather than a scattering of assignments, so that a
  // new index cannot be added in one place and forgotten in the other.
  function bindGraph() {
    nodes = new Map(graph.nodes.map((n) => [n.id, n]));
    groups = new Map((graph.groups || []).filter((g) => g.axis === axis).map((g) => [g.id, g]));
    // Target text is not a namespace: an entity id may equal an encoded edge
    // key, so the discriminator decides which conflicts belong to a box.
    entityConflicts = (graph.conflicts || []).filter((c) => c.target_kind === 'entity');
    edgeConflicts = (graph.conflicts || []).filter((c) => c.target_kind === 'edge');
    contestedEntities = new Set(entityConflicts.map((c) => c.target));
    opens = new Map(((page && page.opens) || []).map((o) => [o.element, o]));
    labelsBySource.clear();
    for (const record of (graph.log_records || [])) {
      if (!labelsBySource.has(record.source)) labelsBySource.set(record.source, new Set());
      for (const label of (record.labels || [])) labelsBySource.get(record.source).add(label);
    }
  }
  bindGraph();

  function currentObservations(id) {
    return (graph.observations || []).filter((o) => {
      if (id && o.subject !== id) return false;
      return !observationCutoff || !o.observed_at || Date.parse(o.observed_at) <= observationCutoff;
    });
  }

  /* ---- text measurement ------------------------------------------------
     ELK does no text measurement, so a node's size has to be decided before
     layout. The canvas measures the font the browser will actually draw with,
     which is the whole difficulty the SVG path has and cannot solve. */

  const ruler = document.createElement('canvas').getContext('2d');
  const measure = (text, px, weight = '') => {
    ruler.font = `${weight} ${px}px Helvetica, Arial, sans-serif`.trim();
    return ruler.measureText(text).width;
  };

  const PAD_X = 14, PAD_Y = 9, LINE = 14;
  // Every box is the same height. Sizing it by how much text it happens to
  // hold made a row of boxes ragged and said something about the box that
  // was not true — a two-line name is not a bigger thing than a one-line one.
  // Tall enough for two lines and the glyph, which is what nearly all of them
  // have; a name longer than that is drawn inside the same box.
  const BOX_HEIGHT = 46;
  const ICON = 15, ICON_GAP = 8;

  function nodeLabels(n) {
    let second = shortType(n.type);
    // A container drawn as one box has to say how much it stands for, or a
    // namespace holding two hundred things looks like a namespace holding one.
    if (n.attrs && n.attrs.container && typeof n.attrs.members === 'number') {
      second += ` · ${n.attrs.members}`;
    }
    const cov = COVERAGE[state(n)];
    if (cov && cov.badge) second += ' · ' + cov.badge;
    const name = nameOf(n);
    const first = name && name !== shortType(n.type) ? name : '';
    return first ? [first, second] : [second];
  }

  function nodeSize(n) {
    const lines = nodeLabels(n);
    const w = Math.max(...lines.map((l, i) => measure(l, i === 0 ? 12 : 11, i === 0 ? '600' : '')));
    const sized = sizes.get(n.id) || {};
    // Room for the chevron on a box that has an inside, or it is drawn over
    // the end of the name.
    const door = openingFor(n.id) ? 18 : 0;
    return {
      width: sized.width || Math.max(104, Math.ceil(w) + PAD_X * 2 + ICON + ICON_GAP + door),
      height: sized.height || BOX_HEIGHT,
    };
  }

  // iconFor prefers a glyph for the exact resource type, so a directory of
  // per-service icons is used at full fidelity, and falls back to the
  // category, which is all the built-in set has.
  function iconFor(type) {
    if (document.getElementById('icon-' + type)) return 'icon-' + type;
    return 'icon-' + categoryOf(type);
  }

  /* ---- building the ELK tree ------------------------------------------ */

  function visibleNode(n) {
    if (focusNodes && !focusNodes.has(n.id)) return false;
    const s = state(n);
    if (s && hidden.has(s)) return false;
    if (!s && hidden.has('none')) return false;
    const labels = labelsBySource.get(n.id);
    if (labels) for (const label of hiddenLabels) if (labels.has(label)) return false;
    return true;
  }

  // insideCollapsed walks up the group chain: anything under a folded
  // container is not laid out at all, which is what makes folding worth doing
  // on a large estate rather than merely tidier.
  function insideCollapsed(path) {
    if (!path) return false;
    const parts = path.split('/');
    for (let i = 0; i < parts.length; i++) {
      if (collapsed.has(parts[i])) return i < parts.length - 1 || false;
    }
    return false;
  }

  function buildGroup(g) {
    const path = groupPath(g.id);
    const label = (g.type || '') + (g.label ? ': ' + g.label : '');
    const folded = collapsed.has(g.id);

    const kids = [];
    if (!folded) {
      for (const child of childGroups(g.id)) kids.push(buildGroup(child));
      for (const n of nodesIn(path)) if (visibleNode(n)) kids.push(buildNode(n));
    }

    const labelWidth = measure(label, 11, '600') + 30;

    // A container with no children needs a size of its own. ELK cannot lay
    // out an empty box that has not been given one, and it should not have to
    // — an empty subnet still gets a border here, because an empty subnet is
    // worth noticing and dropping it would quietly rewrite the topology.
    const bare = kids.length === 0;

    // Spread rather than a conditional value: ELK fails on a `width` key that
    // is present and undefined — with "Cannot read properties of null", from
    // deep inside the algorithm, which is a long way from the cause. A
    // container that sizes itself must not carry the key at all.
    const sized = folded || bare ? {width: Math.max(140, labelWidth), height: 40} : {};

    return {
      id: 'group:' + g.id,
      infra: {kind: 'group', group: g, label, folded, count: countIn(g.id), empty: bare && !folded},
      children: kids,
      ...sized,
      layoutOptions: {
        'elk.padding': '[top=30.0,left=14.0,bottom=14.0,right=14.0]',
      },
    };
  }

  function countIn(groupID) {
    let n = 0;
    const walk = (id) => {
      n += nodesIn(groupPath(id)).length;
      for (const c of childGroups(id)) walk(c.id);
    };
    walk(groupID);
    return n;
  }

  function buildNode(n) {
    const size = nodeSize(n);
    return {id: 'node:' + n.id, infra: {kind: 'node', node: n}, ...size};
  }

  function buildRoot() {
    const children = [];
    for (const g of childGroups(null)) children.push(buildGroup(g));
    for (const n of nodesIn('')) if (visibleNode(n)) children.push(buildNode(n));

    const drawn = new Set();
    const collect = (c) => {
      drawn.add(c.id);
      (c.children || []).forEach(collect);
    };
    children.forEach(collect);

    // An edge whose endpoint is folded away reattaches to the nearest visible
    // ancestor, so folding a container summarises its traffic instead of
    // silently deleting it.
    const edges = [];
    for (const [i, e] of allEdges().entries()) {
      const from = anchorFor(e.from, drawn);
      const to = anchorFor(e.to, drawn);
      if (!from || !to || from === to) continue;
      edges.push({id: 'e' + i, sources: [from], targets: [to], infra: {kind: 'edge', edge: e}});
    }

    return {
      id: 'root',
      children,
      edges,
      layoutOptions: {
        'elk.algorithm': 'layered',
        // A call chain reads down the page. Everything else keeps the
        // direction the document was generated with.
        'elk.direction': page && page.kind === 'sequence' ? 'DOWN' : layoutDirection,
        'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
        'elk.spacing.nodeNode': '26',
        'elk.layered.spacing.nodeNodeBetweenLayers': '46',
        'elk.edgeRouting': lineShape === 'orthogonal' ? 'ORTHOGONAL' : 'SPLINES',
      },
    };
  }

  const allEdges = () => graph.edges
    .filter((e) => requestedKinds.size === 0 || requestedKinds.has(e.kind))
    .concat(pending.map((p) => p.edge).filter(Boolean));

  function anchorFor(id, drawn) {
    if (drawn.has('node:' + id)) return 'node:' + id;
    if (drawn.has('group:' + id)) return 'group:' + id;

    const n = nodes.get(id);
    if (n) {
      if (!visibleNode(n)) return null;
      const path = (n.groups || {})[axis] || '';
      for (const part of path.split('/').filter(Boolean)) {
        if (collapsed.has(part)) return 'group:' + part;
      }
    }
    if (groups.has(id)) {
      for (const part of groupPath(id).split('/')) {
        if (collapsed.has(part)) return 'group:' + part;
      }
    }
    return null;
  }

  /* ---- the canvas -------------------------------------------------------
     maxGraph draws, and owns every gesture that touches a box or a line. ELK
     still decides where things go: of the two it is the one that lays out
     nested containers well, and its answer is handed over as coordinates.

     What stays here is what the drawing means. A colour, a dash and a glyph
     each say something about coverage, provenance or kind, so they are decided
     in this file and handed to maxGraph as styles. */

  const {Graph, InternalEvent, RectangleShape, RubberBandHandler, ShapeRegistry, Point} = window.maxGraph;
  const canvas = document.getElementById('canvas');
  const NS = 'http://www.w3.org/2000/svg';
  const el = (name, attrs = {}) => {
    const e = document.createElementNS(NS, name);
    for (const [k, v] of Object.entries(attrs)) if (v !== undefined && v !== '') e.setAttribute(k, v);
    return e;
  };

  // The glyph and the label are drawn here rather than left to maxGraph. The
  // glyph has to come out of the sprite the page already embeds: maxGraph's
  // `image` style wants a URL, and a data: URI is refused by an ordinary
  // Content-Security-Policy, so the shape reaches into the sprite with <use>.
  // The label is two lines that are not one label in two rows — the name is
  // the subject and the type is a note about it, and they carry different
  // weight and size to say so.
  //
  // A shape paints in graph coordinates and the canvas applies the view's
  // scale and translate on the way out. Elements put into the node by hand
  // miss that, so they carry it themselves: without it the boxes shrink to fit
  // and their labels stay where they were, the full size, somewhere off to the
  // side of the diagram.
  const decorate = (node, make) => {
    for (const old of [...node.querySelectorAll('[data-ig]')]) old.remove();
    for (const e of make) { e.setAttribute('data-ig', ''); node.appendChild(e); }
  };

  const screen = (c) => {
    const st = (c && c.state) || {};
    const s = st.scale || 1;
    return {s, at: (x, y) => [(x + (st.dx || 0)) * s, (y + (st.dy || 0)) * s]};
  };

  class BoxShape extends RectangleShape {
    paintVertexShape(c, x, y, w, h) {
      super.paintVertexShape(c, x, y, w, h);
      if (!this.node) return;
      const st = this.style || {};
      const {s, at} = screen(c);
      const parts = [];

      if (st.icon) {
        const [ix, iy] = at(x + PAD_X, y + (h - ICON) / 2);
        parts.push(el('use', {
          href: '#' + st.icon, x: ix, y: iy, width: ICON * s, height: ICON * s,
          color: this.stroke, opacity: 0.85,
        }));
      }

      const lines = (st.lines || '').split('\n').filter(Boolean);
      const top = y + (h - lines.length * LINE) / 2;
      lines.forEach((line, i) => {
        const [tx, ty] = at(x + PAD_X + ICON + ICON_GAP, top + LINE * i + 11);
        const t = el('text', {
          x: tx, y: ty,
          'font-size': (i === 0 ? 12 : 11) * s,
          'font-weight': i === 0 ? 600 : 400,
          'font-family': 'Helvetica, Arial, sans-serif',
          fill: st.labelColor || '#1d2126',
        });
        t.textContent = line;
        parts.push(t);
      });

      if (st.opens) {
        const r = 5.5;
        const [cx, cy] = at(x + w - PAD_X, y + h / 2);
        parts.push(el('circle', {
          cx, cy, r: r * s, fill: 'none', stroke: this.stroke, 'stroke-width': 1.1 * s, opacity: 0.7,
        }));
        const chevron = el('path', {
          d: `M ${cx - 1.6 * s} ${cy - 2.6 * s} L ${cx + 1.6 * s} ${cy} L ${cx - 1.6 * s} ${cy + 2.6 * s}`,
          fill: 'none', stroke: this.stroke, 'stroke-width': 1.4 * s,
          'stroke-linecap': 'round', 'stroke-linejoin': 'round',
        });
        parts.push(chevron);
      }
      decorate(this.node, parts);
    }
  }

  class GroupShape extends RectangleShape {
    paintVertexShape(c, x, y, w, h) {
      super.paintVertexShape(c, x, y, w, h);
      if (!this.node) return;
      const st = this.style || {};
      const {s, at} = screen(c);
      const [tx, ty] = at(x + 12, y + 19);
      const t = el('text', {
        x: tx, y: ty, class: 'group-label',
        'font-size': 11 * s, 'font-weight': 600,
        'font-family': 'Helvetica, Arial, sans-serif',
        fill: st.labelColor || '#4a5763',
      });
      t.textContent = st.lines || '';
      decorate(this.node, [t]);
    }
  }

  ShapeRegistry.add('oekaki-box', BoxShape);
  ShapeRegistry.add('oekaki-group', GroupShape);

  const board = new Graph(canvas);
  board.setPanning(true);
  board.setCellsResizable(false);
  board.setCellsEditable(false);
  board.setCellsBendable(false);
  board.setCellsDisconnectable(false);
  board.setConnectable(false);
  board.setHtmlLabels(false);
  board.setAllowDanglingEdges(false);
  board.setCellsMovable(false);     // read mode; setMode turns this on
  // Folding is this page's own: the container's title is the control, and the
  // fold is a rebuild of the layout rather than a change to a cell. maxGraph's
  // own handle would sit on top of that offering a second answer, and it
  // fetches an image the page does not ship to draw itself with.
  board.isCellFoldable = () => false;
  board.setDropEnabled(false);
  board.setSplitEnabled(false);
  // ELK has already sized every container to hold its children. Left to grow
  // parents on its own, maxGraph resizes the container as each child arrives,
  // which is both a fight with the layout and, with containers nested inside
  // containers, a resize that feeds itself.
  board.setExtendParents(false);
  board.setExtendParentsOnAdd(false);
  board.setConstrainChildren(false);
  board.setAutoSizeCells(false);
  board.setRecursiveResize(false);
  // A line drawn for one position of a box says nothing once the box is
  // elsewhere. Dropping the route rather than dragging its bends along is the
  // same answer this viewer reached by hand before maxGraph arrived.
  board.resetEdgesOnMove = true;

  // The line's tooltip is where an edge says who claimed it and whether
  // somebody asserted it does not exist. That is not decoration: it is the
  // only place a reader sees the provenance of a line without selecting one
  // of its ends.
  board.setTooltips(true);
  board.getTooltipForCell = (cell) => {
    const infra = cell && cell.infra;
    if (!infra) return '';
    if (infra.kind === 'edge') return edgeTooltip(infra.edge);
    if (infra.kind === 'node') return infra.node.claim ? claimLine(infra.node.claim) : '';
    return '';
  };

  const plugin = (name) => (typeof board.getPlugin === 'function' ? board.getPlugin(name) : null);
  const panning = plugin('PanningHandler');
  if (panning) {
    panning.useLeftButtonForPanning = true;
    // What a drag means depends on what is under it and which mode this is.
    //
    // Reading, it always moves the view. Editing, it moves the box under the
    // pointer — but only a box. A container is not something you place; it is
    // where boxes are, and in a nested diagram containers cover most of the
    // canvas, so a drag that starts on one has to move the view or there is
    // hardly anywhere left to pan from.
    //
    // Forcing the pan consumes the press, which is also what stops maxGraph
    // from picking a container up and putting it down somewhere no layout
    // knows about. A click is unaffected: it is decided on release.
    // The right button always moves the view, in either mode, which is the
    // one gesture that never has to be looked up. Reading, the left button
    // moves it too. Editing, the left button belongs to the drawing — a drag
    // on the canvas draws a box round what it covers and a drag on a box moves
    // the box — so the view moves on the right button or ⌥-drag.
    const panGesture = (me) => {
      const evt = me.getEvent();
      return evt.button === 2 || evt.altKey || !editing;
    };
    panning.isForcePanningEvent = panGesture;
    // The trigger has to say the same thing. It answers a press on empty
    // canvas on its own, which is exactly where the box round several boxes
    // is drawn from — leave it alone and panning eats that gesture before the
    // rubber band ever sees it.
    panning.isPanningTrigger = panGesture;
  }
  const selectionHandler = plugin('SelectionHandler');
  if (selectionHandler) {
    selectionHandler.guidesEnabled = true;
    // On a press maxGraph walks up from the cell under the pointer to the
    // outermost selectable ancestor and moves that one. In a nested diagram
    // that is the VPC: a drag meant for one box would carry off every box in
    // it. A box is the thing this viewer places, so the press stays with the
    // box it landed on.
    selectionHandler.getInitialCellForEvent = (me) => me.getCell();
  }

  // Picking several boxes is this page's own affair. maxGraph's toggle runs
  // after the click has been answered and undoes it — measured: the click
  // marks the box, and a moment later the toggle takes it straight back off,
  // leaving nothing selected. Told there is no toggle, maxGraph marks the box
  // under the pointer and leaves the rest to the click.
  board.isToggleEvent = () => false;

  // Drawing a box round several. It is off while reading, where a drag on the
  // canvas moves the view instead.
  const rubberBand = new RubberBandHandler(board);
  rubberBand.setEnabled(false);

  // The right button is a gesture here, so the menu it usually opens is not.
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());

  const cells = new Map();          // graph node or group id -> maxGraph cell
  const edgeCells = new Map();      // edge key -> maxGraph cell
  const edgeInfo = new Map();       // edge key -> the edge it was drawn from
  const edgeEnds = new Map();       // edge key -> the two ids it was drawn between
  const elkRoutes = new Map();      // edge key -> the route ELK gave it, if any
  // Cells are positioned inside their container; a route is a line across the
  // whole canvas. This is every drawn box and container in the one coordinate
  // system a route can be written in.
  const absRect = new Map();
  // Where the layout put each box, kept apart from where it ended up. A box a
  // hand moved has both: this is the answer the engine gave, which is what a
  // fresh layout document is made of.
  const computed = new Map();
  let fitted = false;
  let minK = 0.1;

  // Selecting is not a change to the diagram, so it does not go through the
  // layout: maxGraph marks the cell and nothing else is touched. This is the
  // whole reason for the move — the old viewer answered a click on a box by
  // laying the graph out again.
  function highlight(cell) {
    picked.clear();
    if (cell) board.setSelectionCell(cell); else board.clearSelection();
  }

  // Marks every picked box. A box a filter has taken out of the drawing drops
  // out of the marking without being forgotten: it is marked again when it
  // comes back.
  let marking = false;
  function markPicked() {
    marking = true;
    const marks = [...picked].map((id) => cells.get(id)).filter(Boolean);
    if (marks.length) board.setSelectionCells(marks); else board.clearSelection();
    marking = false;
  }

  // A box round several boxes is maxGraph's gesture, not this page's, so it
  // changes the selection without going through select(). Rather than keep two
  // answers to "what is picked" and hope they agree, the set is read back from
  // whatever maxGraph ends up holding.
  board.getSelectionModel().addListener(InternalEvent.CHANGE, () => {
    if (marking) return;
    picked.clear();
    for (const cell of board.getSelectionCells()) {
      if (cell.isVertex() && kindOf(cell) === 'node') picked.add(idOf(cell));
    }
    if (!picked.size) return;
    if (!selected || !picked.has(selected)) selected = [...picked][picked.size - 1];
    describePicked();
  });

  /* ---- painting -------------------------------------------------------- */

  function paint(laid) {
    clearError();
    cells.clear();
    edgeCells.clear();
    edgeInfo.clear();
    edgeEnds.clear();
    elkRoutes.clear();
    absRect.clear();
    computed.clear();
    board.getDataModel().clear();
    const root = board.getDefaultParent();
    const edges = [];

    board.batchUpdate(() => {
      placeChildren(root, laid, 0, 0, edges);
      for (const e of laid.edges || []) edges.push({e, ox: 0, oy: 0});
      for (const {e, ox, oy} of edges) placeEdge(root, e, ox, oy);
    });

    // Anchoring needs every box in place, so it comes after the batch rather
    // than inside placeEdge: a line is spread along a side by the company it
    // keeps there, which is not known until the last one is drawn.
    applyAnchors();

    // A repaint builds new cells, so whatever was selected has to be marked
    // again — otherwise folding a container silently drops the highlight on
    // the box whose details are still on screen beside it.
    if (picked.size) markPicked();
    else if (selectedGroup && cells.has(selectedGroup)) highlight(cells.get(selectedGroup));
    else if (selectedEdge && edgeCells.has(selectedEdge)) highlight(edgeCells.get(selectedEdge));

    if (!fitted) fitted = fit(laid);
    // A layout has just landed, so there is now a picture to hand over. The
    // control that offers it is decided from what exists, and until this ran
    // nothing did.
    syncTools();
    document.getElementById('status').textContent = summary();
  }

  // ELK hands back coordinates relative to each container, and so does
  // maxGraph, so children go in unchanged. The absolute offset is carried
  // along anyway because an edge's route is relative to the container ELK
  // attached the edge to, which is not the one its endpoints sit in.
  function placeChildren(parent, container, ax, ay, edges) {
    for (const c of container.children || []) {
      const x = c.x || 0, y = c.y || 0;
      const cell = c.infra.kind === 'group'
        ? placeGroup(parent, c, x, y)
        : placeBox(parent, c, x, y);
      // Where the cell actually went, which is not where ELK put it when the
      // box was placed by hand.
      const geo = cell.getGeometry();
      absRect.set(idOf(cell), {x: ax + geo.x, y: ay + geo.y, w: geo.width, h: geo.height});
      // Where the box would have gone if nobody had touched it, written in
      // the same terms a layout document uses: relative to whatever contains
      // it, named by that container. Nothing recorded this before, and it is
      // the reason reading a diagram could not produce a layout — the only
      // positions the page could name were the ones a hand had already moved,
      // so the document came out empty and the control was hidden.
      if (c.infra.kind === 'node') {
        computed.set(idOf(cell), {
          id: idOf(cell),
          parent: String(parent.id || '').startsWith('group:') ? idOf(parent) : undefined,
          x, y,
        });
      }
      placeChildren(cell, c, ax + geo.x, ay + geo.y, edges);
      for (const e of c.edges || []) edges.push({e, ox: ax + x, oy: ay + y});
    }
  }

  function placeGroup(parent, c, x, y) {
    const g = c.infra.group;
    const st = groupStyle(g.type);
    const label = c.infra.folded ? `${c.infra.label}  (${c.infra.count} folded)`
      : c.infra.empty ? `${c.infra.label}  (empty)` : c.infra.label;

    const cell = board.insertVertex({
      parent, id: 'group:' + g.id, value: '',
      position: [x, y], size: [c.width, c.height],
      style: {
        shape: 'oekaki-group', noLabel: true, rounded: true, arcSize: 8,
        fillColor: st.fill, strokeColor: st.stroke, strokeWidth: 1.4,
        labelColor: st.text, lines: label, group: g.id,
        // A container is placed by the layout, never by hand. Nothing records
        // where it was dropped, so a move would be undone by the next repaint.
        movable: false,
      },
    });
    cell.infra = c.infra;
    cells.set(g.id, cell);
    return cell;
  }

  function placeBox(parent, c, x, y) {
    const n = c.infra.node;
    // A level draws the containers inside it as boxes, and a container that
    // looks like a resource is a container nobody opens. It keeps the colours
    // it has when it is nested, so the same namespace is the same shade
    // whichever way it is being drawn.
    const asContainer = n.attrs && n.attrs.container ? groupStyle(n.type) : null;
    const cat = asContainer || CATEGORY[categoryOf(n.type)] || CATEGORY.generic;
    const cov = COVERAGE[state(n)];
    const abnormal = currentObservations(n.id).some((o) => ['abnormal', 'critical', 'alert'].includes(o.state));
    const stroke = abnormal ? '#c74f63' : (cov && cov.stroke ? cov.stroke : cat.stroke);
    const dashed = (cov && cov.dashed) || (n.claim && n.claim.origin !== 'parser');

    // A placement is stored relative to the container it was made in. Folding
    // or filtering can hand this box to a different parent, and applying the
    // old numbers there would put it somewhere nobody chose.
    const placed = positions.get(n.id);
    const container = parent && String(parent.id || '').startsWith('group:')
      ? String(parent.id).replace(/^group:/, '') : undefined;
    const pinned = placed && (placed.parent || undefined) === container ? placed : null;

    const cell = board.insertVertex({
      parent, id: 'node:' + n.id, value: '',
      position: pinned ? [pinned.x, pinned.y] : [x, y],
      size: [c.width, c.height],
      style: {
        shape: 'oekaki-box', noLabel: true, rounded: true, arcSize: 6,
        fillColor: cat.fill, strokeColor: stroke,
        strokeWidth: contestedEntities.has(n.id) || abnormal ? 2.6 : (cov && cov.width ? cov.width : 1.2),
        dashed, dashPattern: '5 3',
        icon: iconFor(n.type), lines: nodeLabels(n).join('\n'), labelColor: cat.text,
        // Drawn as a chevron on the right edge. Without it the only way to
        // learn that a box has an inside is to try it, and a reader who tries
        // two boxes that have none stops trying the third.
        opens: openingFor(n.id) ? 1 : 0,
        // A page about one element draws two different things: what is in it,
        // and what it talks to. The second is context for reading the first,
        // and drawing them identically answers the reader's question — "what
        // is in this box" — with a list that is mostly not in it.
        opacity: n.attrs && n.attrs.inside === false ? 55 : 100,
      },
    });
    cell.infra = c.infra;
    cells.set(n.id, cell);
    return cell;
  }

  function placeEdge(parent, e, ox, oy) {
    const edge = e.infra.edge;
    const st = edge.suppressed ? SUPPRESSED : (EDGE[edge.kind] || EDGE.iac_ref);
    const asserted = edge.claim && edge.claim.origin !== 'parser';
    const from = cells.get(edge.from) || cells.get(anchorId(e.sources));
    const to = cells.get(edge.to) || cells.get(anchorId(e.targets));
    if (!from || !to) return;

    // On a sequence the order is the content: the same three lines drawn
    // without their numbers are a picture of who calls whom, which the reader
    // already had one level up.
    const step = edge.attrs && edge.attrs.step;
    const cell = board.insertEdge({
      parent, source: from, target: to, value: step ? String(step) : '',
      style: {
        noLabel: !step, fontSize: 11, fontColor: st.color, labelBackgroundColor: '#ffffff',
        strokeColor: st.color, strokeWidth: st.width,
        dashed: !!st.dash, dashPattern: st.dash || undefined,
        endArrow: 'block', endSize: 7,
        // A claimed line keeps a hollow head: somebody said this exists, and
        // the drawing should not pass that off as something a parser found.
        endFill: asserted ? 0 : 1,
        rounded: true,
      },
    });
    cell.infra = e.infra;
    const key = edgeKey(edge);
    const fromID = idOf(from), toID = idOf(to);
    edgeCells.set(key, cell);
    edgeInfo.set(key, edge);
    edgeEnds.set(key, [fromID, toID]);

    // ELK's route, unless one of its ends was put somewhere by hand — then the
    // bends belong to a position that box no longer has, and the line is drawn
    // in the same shape ELK would have given it.
    const section = (e.sections || [])[0];
    const placed = positions.has(edge.from) || positions.has(edge.to);
    // ELK's route is kept even when this pass does not use it: taking a side
    // back by hand has to be able to put the line back the way it was found,
    // and a layout is not run for that.
    if (section) {
      const bends = (section.bendPoints || []).map((p) => new Point(ox + p.x, oy + p.y));
      if (bends.length) {
        elkRoutes.set(key, bends);
        if (!placed && !edgeAnchors.has(key)) cell.geometry.points = bends;
      }
    }
  }

  const anchorId = (ends) => String((ends || [])[0] || '').replace(/^(node|group):/, '');

  /* ---- where a line meets a box ---------------------------------------
     Nothing recorded this before. Which side a line left and arrived on fell
     out of ELK's route and was lost the moment the route was — so moving one
     box collapsed every line on it onto two points, and there was no way to
     say otherwise.

     maxGraph decides an unanchored end by the angle from the box's centre to
     the next point on the line, and a fixed anchor (exitX / entryX) overrides
     it. That is the automatic-or-by-hand split already built into the library:
     a side that was chosen is written as a fixed anchor, and a side that was
     not is worked out here from where the two boxes ended up. */

  const SIDES = ['left', 'right', 'top', 'bottom'];

  // Which sides face each other.
  //
  // The flow decides first. A diagram laid out left to right is read left to
  // right, and a line leaving the top of a box reads as going somewhere else
  // — so as long as the two boxes are clear of each other along the flow, the
  // line leaves the trailing side and arrives on the leading one, however far
  // apart they are across it.
  //
  // Comparing the two gaps instead, which is what this did at first, sent a
  // line out of the top whenever the boxes were further apart down the page
  // than along it. On a tall diagram that is most of them, and a page of
  // lines leaving in every direction is the thing that makes one hard to
  // read.
  //
  // Only when the boxes overlap along the flow — one above the other in a
  // left-to-right diagram — has the flow nothing to say, and then the other
  // axis decides.
  function facingSides(from, to) {
    const down = layoutDirection === 'DOWN';
    const dx = (to.x + to.w / 2) - (from.x + from.w / 2);
    const dy = (to.y + to.h / 2) - (from.y + from.h / 2);
    const alongFlow = down
      ? (dy > 0 ? {source: 'bottom', target: 'top'} : {source: 'top', target: 'bottom'})
      : (dx > 0 ? {source: 'right', target: 'left'} : {source: 'left', target: 'right'});
    const acrossFlow = down
      ? (dx >= 0 ? {source: 'right', target: 'left'} : {source: 'left', target: 'right'})
      : (dy >= 0 ? {source: 'bottom', target: 'top'} : {source: 'top', target: 'bottom'});

    const clear = down
      ? (dy > 0 ? to.y - (from.y + from.h) : from.y - (to.y + to.h))
      : (dx > 0 ? to.x - (from.x + from.w) : from.x - (to.x + to.w));
    return clear > 0 ? alongFlow : acrossFlow;
  }

  const vertical = (side) => side === 'left' || side === 'right';
  // A fraction of the box, which is what maxGraph's exitX/entryX want.
  const fractionOn = (side, at) => side === 'left' ? [0, at] : side === 'right' ? [1, at]
    : side === 'top' ? [at, 0] : [at, 1];
  const pointOn = (rect, side, at) => vertical(side)
    ? {x: side === 'left' ? rect.x : rect.x + rect.w, y: rect.y + rect.h * at}
    : {x: rect.x + rect.w * at, y: side === 'top' ? rect.y : rect.y + rect.h};
  const away = (side) => side === 'left' ? {x: -1, y: 0} : side === 'right' ? {x: 1, y: 0}
    : side === 'top' ? {x: 0, y: -1} : {x: 0, y: 1};

  // ELK routes with splines: a line leaves a box, curves once, and is straight
  // on again by the time it arrives. A line drawn here has to look like those,
  // or the boxes that were touched read as a different drawing. So it is a
  // cubic that leaves and arrives square to the sides it is anchored on,
  // sampled into the waypoints maxGraph joins up.
  // Two lines given the same two points draw the same line. A channel pulls
  // them apart in the middle: it is where the line sits among the ones sharing
  // its sides, from -1 to 1, which is already distinct for each of them.
  // Which lane the line takes its channel from: the crowded one. Within a lane
  // the fractions were handed out in the order the other ends sit in, so using
  // one lane's order keeps the bundle nested. Adding the two orders together —
  // which is what this did at first — mixes two orderings that have nothing to
  // do with each other, and the lines cross where they disagree: measured on
  // a busy box, adding the two orders together left more crossings than
  // having no ordering at all, and this one roughly halves them.
  const channelOf = (r) => 1 - 2 * ((r.targetLane || 1) >= (r.sourceLane || 1) ? r.targetAt : r.sourceAt);

  function curveBetween(a, b, fromSide, toSide, channel, bypass) {
    const oa = away(fromSide), ob = away(toSide);
    // How far the line holds its course before turning. Proportional to the
    // gap so a short hop is not made to bulge, with a floor so two boxes that
    // nearly touch still leave and arrive square.
    const k = Math.max(40, Math.hypot(b.x - a.x, b.y - a.y) * 0.3);
    // Bowed sideways by the channel, so two lines running the same way bow by
    // different amounts instead of lying on top of each other.
    const span = Math.hypot(b.x - a.x, b.y - a.y) || 1;
    const bow = Math.min(45, span * 0.14) * channel;
    const px = -(b.y - a.y) / span * bow, py = (b.x - a.x) / span * bow;
    const c1 = {x: a.x + oa.x * k + px, y: a.y + oa.y * k + py};
    const c2 = {x: b.x + ob.x * k + px, y: b.y + ob.y * k + py};
    // Doubling back: the handles are pulled out to the way round, so the curve
    // bulges past the boxes instead of cutting between them.
    if (bypass !== undefined) {
      if (vertical(fromSide)) { c1.y = bypass; c2.y = bypass; }
      else { c1.x = bypass; c2.x = bypass; }
    }

    const STEPS = 10;
    const points = [];
    for (let i = 1; i < STEPS; i++) {
      const t = i / STEPS, u = 1 - t;
      points.push(new Point(
        u * u * u * a.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t * t * t * b.x,
        u * u * u * a.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t * t * t * b.y));
    }
    return points;
  }

  // Right angles between the same two anchors. Two ends facing each other
  // across a gap turn once in the middle of it; two ends on the same side have
  // to come out, run past each other and go back in; an end on a side at right
  // angles to the other's needs one corner, level with the horizontal end.
  // A line whose two ends face away from each other has to double back. Doing
  // that between the boxes runs it straight through everything else crossing
  // the same gap — which on this estate is a fan of thirty. Outside them it
  // crosses nothing: the way round is chosen for the shorter climb, and the
  // channel keeps several of them apart.
  function bypassLine(from, to, a, b, sideways, channel) {
    const lead = 26 + 18 * Math.abs(channel);
    if (sideways) {
      const above = Math.min(from.y, to.y) - lead;
      const below = Math.max(from.y + from.h, to.y + to.h) + lead;
      return Math.abs(a.y - above) + Math.abs(b.y - above)
        <= Math.abs(a.y - below) + Math.abs(b.y - below) ? above : below;
    }
    const left = Math.min(from.x, to.x) - lead;
    const right = Math.max(from.x + from.w, to.x + to.w) + lead;
    return Math.abs(a.x - left) + Math.abs(b.x - left)
      <= Math.abs(a.x - right) + Math.abs(b.x - right) ? left : right;
  }

  function elbowBetween(a, b, fromSide, toSide, channel, bypass) {
    const P = (x, y) => new Point(x, y);
    const oa = away(fromSide), ob = away(toSide);
    // Where this line turns. Every line turning at the midpoint runs down the
    // same column, which on a page of right angles is nearly all of them:
    // measured, moving one box left 54% of the drawn line lying on another,
    // seven deep at the worst place. The channel gives each its own column.
    const lane = (span) => Math.min(64, Math.max(16, Math.abs(span) * 0.3)) * channel;
    const k = 30 + 22 * Math.abs(channel);

    if (vertical(fromSide) && vertical(toSide)) {
      const facing = (b.x - a.x) * oa.x > 0 && (a.x - b.x) * ob.x > 0;
      if (facing) {
        const mx = (a.x + b.x) / 2 + lane(b.x - a.x);
        return [P(mx, a.y), P(mx, b.y)];
      }
      const out = a.x + oa.x * k, back = b.x + ob.x * k;
      const my = bypass !== undefined ? bypass : (a.y + b.y) / 2 + lane(b.y - a.y);
      return [P(out, a.y), P(out, my), P(back, my), P(back, b.y)];
    }
    if (!vertical(fromSide) && !vertical(toSide)) {
      const facing = (b.y - a.y) * oa.y > 0 && (a.y - b.y) * ob.y > 0;
      if (facing) {
        const my = (a.y + b.y) / 2 + lane(b.y - a.y);
        return [P(a.x, my), P(b.x, my)];
      }
      const out = a.y + oa.y * k, back = b.y + ob.y * k;
      const mx = bypass !== undefined ? bypass : (a.x + b.x) / 2 + lane(b.x - a.x);
      return [P(a.x, out), P(mx, out), P(mx, back), P(b.x, back)];
    }
    // One corner. Nudging it along the leg the line arrives on keeps two such
    // lines from sharing it.
    return vertical(fromSide)
      ? [P(b.x + lane(b.x - a.x) * 0.4, a.y)]
      : [P(a.x, b.y + lane(b.y - a.y) * 0.4)];
  }

  // applyAnchors decides, for every line this viewer routes itself, which side
  // of each box it meets and where along that side — then writes both the
  // anchors and the curve. It is the whole of the answer to a moved box and to
  // a side chosen by hand, and it runs no layout.
  //
  // A line is routed here when one of its boxes was placed by hand (its ELK
  // route belongs to where that box used to be) or when a side was chosen for
  // it (the ELK route was drawn for a different anchor, so keeping it would
  // leave the middle of the line disagreeing with its ends).
  function applyAnchors() {
    const routed = [];
    for (const [key, ends] of edgeEnds) {
      const e = edgeInfo.get(key);
      const cell = edgeCells.get(key);
      if (!e || !cell) continue;
      const chosen = edgeAnchors.get(key);
      if (!chosen && !positions.has(e.from) && !positions.has(e.to)) continue;
      if (chosen && !chosen.source && !chosen.target && chosen.line === lineShape) continue;
      const from = absRect.get(ends[0]), to = absRect.get(ends[1]);
      if (!from || !to) continue;
      const auto = facingSides(from, to);
      routed.push({key, cell, ends, from, to,
        source: (chosen && chosen.source) || auto.source,
        target: (chosen && chosen.target) || auto.target});
    }

    // Lines that meet the same side of the same box share it out. Without
    // this they all arrive at the same anchor, however many of them there are
    // — measured on a busy box, every line stacked on one point per side.
    const lanes = new Map();
    const lane = (id, side) => {
      const k = id + '\u0000' + side;
      if (!lanes.has(k)) lanes.set(k, {side, members: []});
      return lanes.get(k).members;
    };
    for (const r of routed) {
      lane(r.ends[0], r.source).push({r, role: 'source', other: r.to});
      lane(r.ends[1], r.target).push({r, role: 'target', other: r.from});
    }
    for (const {side, members} of lanes.values()) {
      // Ordered by where the other end is, so the lines fan out instead of
      // crossing each other on the way in.
      const along = (m) => vertical(side) ? m.other.y + m.other.h / 2 : m.other.x + m.other.w / 2;
      members.sort((a, b) => along(a) - along(b) || (a.r.key < b.r.key ? -1 : 1));
      members.forEach((m, i) => {
        m.r[m.role + 'At'] = (i + 1) / (members.length + 1);
        m.r[m.role + 'Lane'] = members.length;
      });
    }

    const model = board.getDataModel();
    const anchored = new Set(routed.map((r) => r.key));
    board.batchUpdate(() => {
      // A side taken back by hand leaves an anchor behind on a line this pass
      // no longer routes, and maxGraph would go on honouring it.
      for (const [key, cell] of edgeCells) {
        if (anchored.has(key) || cell.style.exitX === undefined) continue;
        const style = {...cell.style};
        for (const k of ['exitX', 'exitY', 'exitPerimeter', 'entryX', 'entryY', 'entryPerimeter']) delete style[k];
        model.setStyle(cell, style);
        const geo = cell.getGeometry().clone();
        geo.points = elkRoutes.get(key) || [];
        model.setGeometry(cell, geo);
      }
      for (const r of routed) {
        const [ex, ey] = fractionOn(r.source, r.sourceAt);
        const [tx, ty] = fractionOn(r.target, r.targetAt);
        model.setStyle(r.cell, {...r.cell.style,
          exitX: ex, exitY: ey, exitPerimeter: false,
          entryX: tx, entryY: ty, entryPerimeter: false});
        const geo = r.cell.getGeometry().clone();
        const a = pointOn(r.from, r.source, r.sourceAt);
        const z = pointOn(r.to, r.target, r.targetAt);
        const channel = channelOf(r);
        // Facing sides meet across the gap; sides that face away have to go
        // round, and the way round is outside both boxes.
        const oa = away(r.source), ob = away(r.target);
        const facing = vertical(r.source) === vertical(r.target)
          && (vertical(r.source)
            ? (z.x - a.x) * oa.x > 0 && (a.x - z.x) * ob.x > 0
            : (z.y - a.y) * oa.y > 0 && (a.y - z.y) * ob.y > 0);
        const bypass = (vertical(r.source) === vertical(r.target) && !facing)
          ? bypassLine(r.from, r.to, a, z, vertical(r.source), channel) : undefined;
        geo.points = lineShapeOf(r.key) === 'orthogonal'
          ? elbowBetween(a, z, r.source, r.target, channel, bypass)
          : curveBetween(a, z, r.source, r.target, channel, bypass);
        model.setGeometry(r.cell, geo);
      }
    });
  }

  // Choosing a side is a fact about the picture, so it goes into the layout
  // document and nothing else. An empty side means "work it out".
  function setAnchor(key, role, value) {
    const e = edgeInfo.get(key);
    if (!e) return;
    const chosen = {...(edgeAnchors.get(key) || {}),
      from: e.from, to: e.to, kind: e.kind, relation: e.relation || ''};
    if (value) chosen[role] = value; else delete chosen[role];
    if (chosen.source || chosen.target || chosen.line) edgeAnchors.set(key, chosen);
    else edgeAnchors.delete(key);
    applyAnchors();
    syncTools();
  }

  // The diagram's own answer is handed to ELK, so it decides the shape of every
  // line, not only the ones drawn here. That needs a layout.
  function setLineShape(next) {
    if (next === lineShape) return;
    lineShape = next;
    syncTools();
    render();
  }

  // The error is the canvas's only content while it is up, and it goes away
  // the moment something draws. Left to accumulate, a second failure stacks a
  // second line, and a filter that succeeds afterwards leaves the reader
  // looking at a message about a failure that is over.
  const clearError = () => {
    for (const old of [...canvas.querySelectorAll('.canvas-error')]) old.remove();
  };

  function fail(message) {
    clearError();
    board.getDataModel().clear();
    const note = document.createElement('p');
    note.className = 'canvas-error';
    note.textContent = message;
    canvas.append(note);
    document.getElementById('status').textContent = 'layout failed';
  }

  function edgeTooltip(e) {
    const parts = [`${e.from} → ${e.to}  (${e.kind})`];
    if (e.suppressed) parts.push('asserted not to exist');
    if (e.claim) parts.push(claimLine(e.claim));
    return parts.join('\n');
  }

  function claimLine(c) {
    let s = 'asserted';
    if (c.author) s += ' by ' + c.author;
    s += ' (' + c.origin;
    if (c.confidence !== undefined) s += `, confidence ${c.confidence}`;
    s += ')';
    if (c.note) s += ': ' + c.note;
    return s;
  }

  // categoryOf mirrors providers.CategoryOf's heuristics closely enough to
  // colour a box. The IR does not carry the category — classification lives in
  // Go — so a substring guess is what is available here.
  function categoryOf(t) {
    if (/security_group|firewall|_iam_|_kms_/.test(t)) return 'security';
    if (/_db_|_rds_|database|_sql_|dynamodb|elasticache/.test(t)) return 'database';
    if (/_lb|_vpc|subnet|network|gateway|route/.test(t)) return 'network';
    if (/bucket|volume|disk|log_group|log_sink/.test(t)) return 'storage';
    if (/instance|_vm|virtual_machine|container|function|ecs_|eks_|deployment/.test(t)) return 'compute';
    return 'generic';
  }

  /* ---- detail panel ---------------------------------------------------- */

  const detail = document.getElementById('detail');

  // `add` is the ⌘ (or Ctrl) click: it puts a box in with the ones already
  // picked, or takes it back out. Without it, the box replaces them.
  function select(id, neighborhood = false, add = false) {
    if (add && picked.has(id)) picked.delete(id);
    else if (add) picked.add(id);
    else { picked.clear(); picked.add(id); }

    // Taking the last one back out leaves nothing to describe.
    if (!picked.size) { selected = null; detail.hidden = true; board.clearSelection(); return; }
    // Taking out the one the panel was about hands it to another.
    if (!picked.has(id)) id = [...picked][picked.size - 1];
    selected = id;
    selectedGroup = null;
    selectedEdge = null;
    describePicked(neighborhood);
  }

  // The panel for whichever box the picking ended on.
  function describePicked(neighborhood = false) {
    const id = selected;
    const n = nodes.get(id);
    if (!n) { detail.hidden = true; return; }

    detail.hidden = false;
    detail.textContent = '';

    const h = document.createElement('h2');
    h.textContent = nameOf(n) || n.id;
    const sub = document.createElement('div');
    sub.className = 'sub';
    sub.textContent = n.type;
    detail.append(h, sub);

    // The chevron on the box says there is a way in; this is the control that
    // always works. A box on a diagram scaled to fit is a few pixels tall,
    // and a double click on it is a gesture nobody has been told about.
    const open = openingFor(n.id);
    if (open) {
      const s = section('中を見る');
      const button = document.createElement('button');
      button.textContent = (open.label || '開く') + ' →';
      button.title = open.kind;
      button.addEventListener('click', () => openDiagram(open.diagram));
      s.append(button);
      detail.append(s);
    }

    if (editing) detail.append(renameControl(n));

    const description = n.description || `${n.type}「${n.name || n.id}」の構成要素`;
    detail.append(withText(section('概要'), description));

    if (n.coverage) {
      const cov = COVERAGE[n.coverage.state] || COVERAGE.unknown;
      const s = section('logs');
      const line = document.createElement('div');
      line.className = 'state';
      line.style.color = cov.stroke || 'inherit';
      line.textContent = n.coverage.state;
      s.append(line);
      if (n.coverage.reason) s.append(text(n.coverage.reason));
      if (n.coverage.evidence && n.coverage.evidence.length) {
        const ul = document.createElement('ul');
        for (const e of n.coverage.evidence) {
          const li = document.createElement('li');
          let t = e.kind;
          if (e.sink) t += ' → ' + e.sink;
          if (e.records !== undefined) t += ` (${e.records} records)`;
          if (e.via) t += ', via ' + e.via;
          li.textContent = t;
          ul.append(li);
        }
        s.append(ul);
      }
      detail.append(s);
    }

    if (n.claim) detail.append(withText(section('claim'), claimLine(n.claim)));

    const conflicts = entityConflicts.filter((c) => c.target === n.id);
    if (conflicts.length) {
      const s = section('disagreement');
      for (const c of conflicts) {
        const ul = document.createElement('ul');
        for (const v of c.claims) {
          const li = document.createElement('li');
          li.textContent = `${c.field} = ${v.value} — ${claimLine(v.claim)}`;
          ul.append(li);
        }
        s.append(ul);
      }
      detail.append(s);
    }

    if (n.attrs && Object.keys(n.attrs).length) detail.append(defList('attributes', n.attrs));
    if (n.metrics && Object.keys(n.metrics).length) detail.append(defList('metrics', n.metrics));
    const observations = currentObservations(n.id);
    if (observations.length) {
      const s = section('observations');
      const ul = document.createElement('ul');
      for (const o of observations) {
        const li = document.createElement('li');
        let label = o.metric;
        if (o.value !== undefined) label += ` = ${o.value}${o.unit ? ' ' + o.unit : ''}`;
        if (o.state) label += ` [${o.state}]`;
        if (o.observed_at) label += ` @ ${o.observed_at}`;
        if (o.labels && Object.keys(o.labels).length) label += ` {${Object.keys(o.labels).sort().map((k) => `${k}=${o.labels[k]}`).join(', ')}}`;
        li.textContent = label + (o.reason ? ` — ${o.reason}` : '');
        ul.append(li);
      }
      s.append(ul);
      detail.append(s);
      const charts = metricSeries(n.id);
      if (charts.length) {
        const s = section('metric changes');
        for (const series of charts) s.append(sparkline(series));
        detail.append(s);
      }
    }
    const logRecords = (graph.log_records || []).filter((r) => r.source === n.id);
    if (logRecords.length) {
      const s = section('classified logs');
      const ul = document.createElement('ul');
      for (const r of logRecords) {
        const li = document.createElement('li');
        let label = r.id;
        if (r.labels && r.labels.length) label += ` [${r.labels.join(', ')}]`;
        if (r.observed_at) label += ` @ ${r.observed_at}`;
        li.textContent = label;
        if (r.characteristics && Object.keys(r.characteristics).length) {
          li.title = Object.keys(r.characteristics).sort().map((k) => `${k}=${r.characteristics[k]}`).join(', ');
        }
        ul.append(li);
      }
      s.append(ul);
      detail.append(s);
    }
    const reachable = graph.edges.filter((e) => e.from === n.id && (e.kind === 'reachable' || e.relation === 'reachable'));
    if (reachable.length) {
      const s = section('reachable from this node');
      const ul = document.createElement('ul');
      for (const e of reachable) {
        const li = document.createElement('li');
        const attrs = e.attrs || {};
        const protocol = attrs.protocol ? ` ${attrs.protocol}` : '';
        const port = attrs.to_port !== undefined ? `:${attrs.to_port}` : (attrs.port !== undefined ? `:${attrs.port}` : '');
        li.textContent = `${e.to}${protocol}${port}` + (attrs.reason ? ` — ${attrs.reason}` : '');
        ul.append(li);
      }
      s.append(ul);
      detail.append(s);
    }
    if (n.source) {
      detail.append(withText(section('declared in'),
        n.source.file + (n.source.line ? ':' + n.source.line : '')));
    }
    detail.append(withText(section('id'), n.id));

    if (neighborhood) {
      const related = graph.edges.filter((e) => e.from === id || e.to === id);
      if (related.length) {
        const s = section('related');
        const ul = document.createElement('ul');
        for (const e of related) {
          const li = document.createElement('li');
          const relation = e.relation || (e.attrs && e.attrs.relation) || e.kind;
          li.textContent = `${e.from === id ? '→ ' + e.to : '← ' + e.from} (${relation})`;
          ul.append(li);
        }
        s.append(ul);
        detail.append(s);
      }
    }

    // Say so when the panel is about one of several: the others move with it.
    if (picked.size > 1) {
      detail.insertBefore(withText(section('selection'),
        `${picked.size} 個を選択中。ドラッグでまとめて動きます`), detail.children[2]);
    }

    markPicked();
  }

  function metricSeries(id) {
    const byKey = new Map();
    for (const o of currentObservations(id)) {
      if (o.value === undefined || !o.observed_at) continue;
      const labels = Object.entries(o.labels || {}).sort(([a], [b]) => a.localeCompare(b))
        .map(([k, v]) => `${k}=${v}`).join(',');
      const key = o.metric + (labels ? ` {${labels}}` : '');
      if (!byKey.has(key)) byKey.set(key, {key, unit: o.unit || '', points: [], threshold: o.threshold});
      byKey.get(key).points.push(o);
    }
    return [...byKey.values()].filter((s) => s.points.length > 1).sort((a, b) => a.key.localeCompare(b.key));
  }

  function sparkline(series) {
    const wrap = document.createElement('div');
    wrap.className = 'sparkline';
    const heading = document.createElement('div');
    heading.className = 'sparkline-title';
    heading.textContent = series.key + (series.unit ? ` (${series.unit})` : '');
    wrap.append(heading);
    const width = 260, height = 74, pad = 8;
    const svg = el('svg', {viewBox: `0 0 ${width} ${height}`, role: 'img', 'aria-label': series.key});
    const values = series.points.map((p) => p.value);
    const threshold = series.threshold && series.threshold.value;
    const all = threshold === undefined ? values : values.concat([threshold]);
    let min = Math.min(...all), max = Math.max(...all);
    if (min === max) { min -= 1; max += 1; }
    const x = (i) => pad + (width - pad * 2) * (i / Math.max(1, values.length - 1));
    const y = (v) => height - pad - (height - pad * 2) * ((v - min) / (max - min));
    svg.append(el('line', {x1: pad, y1: height - pad, x2: width - pad, y2: height - pad, stroke: '#d5d9df', 'stroke-width': 1}));
    if (threshold !== undefined) {
      svg.append(el('line', {x1: pad, y1: y(threshold), x2: width - pad, y2: y(threshold), stroke: '#c74f63', 'stroke-width': 1.2, 'stroke-dasharray': '4 3'}));
    }
    const points = values.map((v, i) => `${x(i)},${y(v)}`).join(' ');
    svg.append(el('polyline', {points, fill: 'none', stroke: '#3b6fd4', 'stroke-width': 2}));
    values.forEach((v, i) => svg.append(el('circle', {cx: x(i), cy: y(v), r: 2.5, fill: vIsAbnormal(series.points[i]) ? '#c74f63' : '#3b6fd4'})));
    wrap.append(svg);
    return wrap;
  }

  function vIsAbnormal(o) {
    return ['abnormal', 'critical', 'alert'].includes(o.state);
  }

  function focusOn(id) {
    const available = new Set(graph.nodes.map((n) => n.id));
    const selectedNodes = new Set([id]);
    let frontier = [id];
    // A focused diagram shows two relationship hops, enough to explain a
    // service without turning a double-click into another unreadable estate.
    for (let depth = 0; depth < 2; depth++) {
      const next = [];
      for (const current of frontier) {
        for (const e of graph.edges) {
          if (e.from !== current && e.to !== current) continue;
          const other = e.from === current ? e.to : e.from;
          if (available.has(other) && !selectedNodes.has(other)) { selectedNodes.add(other); next.push(other); }
        }
      }
      frontier = next;
    }
    focus = id;
    focusNodes = selectedNodes;
    updateBreadcrumbs();
    select(id, true);
  }

  function nodesUnderGroup(id) {
    const path = groupPath(id);
    const prefix = path ? path + '/' : '';
    return new Set(graph.nodes.filter((n) => {
      const nodePath = (n.groups || {})[axis] || '';
      return nodePath === path || nodePath.startsWith(prefix);
    }).map((n) => n.id));
  }

  function selectGroup(id) {
    selectedGroup = id;
    selected = null;
    selectedEdge = null;
    const g = groups.get(id);
    if (!g) return;
    detail.hidden = false;
    detail.textContent = '';
    const h = document.createElement('h2');
    h.textContent = g.label || g.id;
    const sub = document.createElement('div');
    sub.className = 'sub';
    sub.textContent = `${g.type} · ${g.axis}`;
    detail.append(h, sub);
    detail.append(withText(section('概要'), `${g.type}「${g.label || g.id}」の入れ子コンテナ`));

    // Folding needs a control that does not depend on hitting a few pixels of
    // text: the title is drawn under whatever cells and lines cross it, and on
    // a diagram scaled to fit it is a couple of pixels tall. The band at the
    // top of the container is the shortcut; this is the one that always works.
    const fold = section(collapsed.has(id) ? 'unfold' : 'fold');
    const button = document.createElement('button');
    button.textContent = collapsed.has(id) ? '展開する' : '折り畳む';
    button.addEventListener('click', () => { toggleGroup(id); selectGroup(id); });
    fold.append(button);
    detail.append(fold);

    const members = nodesUnderGroup(id);
    detail.append(withText(section('members'), `${members.size} nodes`));
    if (g.source) detail.append(withText(section('declared in'), g.source.file + (g.source.line ? ':' + g.source.line : '')));
    detail.append(withText(section('id'), g.id));
    highlight(cells.get(id));
  }

  // A line was the one thing on the canvas that answered nothing. Its tooltip
  // says who claimed it; what it *is* — which of the three questions it
  // answers, and the references underneath a single drawn arrow — was written
  // down nowhere a reader could reach.
  function selectEdge(key) {
    selectedEdge = key;
    selected = null;
    selectedGroup = null;
    const e = edgeInfo.get(key);
    if (!e) { detail.hidden = true; return; }

    detail.hidden = false;
    detail.textContent = '';

    const h = document.createElement('h2');
    h.textContent = `${endLabel(e.from)} → ${endLabel(e.to)}`;
    const sub = document.createElement('div');
    sub.className = 'sub';
    sub.textContent = e.relation ? `${e.kind} · ${e.relation}` : e.kind;
    detail.append(h, sub);

    if (editing) detail.append(attachmentControl(key));

    detail.append(withText(section('概要'), edgeMeaning(e)));

    // One arrow is usually many references folded together. The examples are
    // the only place the drawing says which ones.
    const attrs = e.attrs || {};
    if (Array.isArray(attrs.examples) && attrs.examples.length) {
      const s = section('reference paths');
      const ul = document.createElement('ul');
      for (const example of attrs.examples) {
        const li = document.createElement('li');
        li.textContent = example;
        ul.append(li);
      }
      s.append(ul);
      detail.append(s);
    }

    if (Object.keys(attrs).length) detail.append(defList('attributes', attrs));
    if (e.claim) detail.append(withText(section('claim'), claimLine(e.claim)));

    const conflicts = edgeConflicts.filter((c) => c.target === key);
    if (conflicts.length) {
      const s = section('disagreement');
      for (const c of conflicts) {
        const ul = document.createElement('ul');
        for (const v of c.claims) {
          const li = document.createElement('li');
          li.textContent = `${c.field} = ${v.value} — ${claimLine(v.claim)}`;
          ul.append(li);
        }
        s.append(ul);
      }
      detail.append(s);
    }

    const ends = section('ends');
    ends.append(text('from: ' + e.from), text('to: ' + e.to));
    detail.append(ends);

    highlight(edgeCells.get(key));
  }

  // Which side of each box this line meets. Reading, it is not a question the
  // page can answer for you — so the control is only here while editing, like
  // the name field above it.
  //
  // Sides, not points. Where along the side the line lands is shared out with
  // whatever else meets that side, and a point picked by hand would be wrong
  // as soon as another line arrived.
  function attachmentControl(key) {
    const s = section('attachment');
    const chosen = edgeAnchors.get(key) || {};
    for (const [role, label] of [['source', '出口'], ['target', '入口']]) {
      const row = document.createElement('div');
      row.className = 'anchor-row';
      const name = document.createElement('span');
      name.textContent = label;
      row.append(name);
      for (const [side, text] of [['', '自動'], ['left', '左'], ['right', '右'], ['top', '上'], ['bottom', '下']]) {
        row.append(anchorButton(key, role, side, text, (chosen[role] || '') === side));
      }
      s.append(row);
    }

    // The diagram's shape unless this line says otherwise. Choosing one takes
    // the line over from ELK: its route was drawn for the other shape.
    const row = document.createElement('div');
    row.className = 'anchor-row';
    const name = document.createElement('span');
    name.textContent = '形';
    row.append(name);
    for (const [shape, text] of [['', '図に従う'], ['curved', '曲線'], ['orthogonal', 'カクカク']]) {
      row.append(anchorButton(key, 'line', shape, text, (chosen.line || '') === shape));
    }
    s.append(row);
    return s;
  }

  function anchorButton(key, role, value, text, pressed) {
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = text;
    button.setAttribute('aria-pressed', String(pressed));
    button.addEventListener('click', () => { setAnchor(key, role, value); selectEdge(key); });
    return button;
  }

  // The three kinds are three different questions, and a reader who clicks a
  // line is asking which one this line answers. The wording follows the IR's
  // own definitions rather than inventing a fourth reading of an arrow.
  function edgeMeaning(e) {
    const attrs = e.attrs || {};
    const from = endLabel(e.from), to = endLabel(e.to);
    const how = wire(attrs);
    let s;
    if (e.kind === 'reachable') {
      s = `ネットワークは ${from} から ${to} への通信を許可している${how ? `（${how}）` : ''}。実際に流れているかは別`;
    } else if (e.kind === 'observed') {
      s = `${from} から ${to} への通信が実測された${how ? `（${how}）` : ''}`;
    } else if (e.relation === 'remote_state') {
      s = `${from} が terraform_remote_state で ${to} の出力を読んでいる。${to} を消すと ${from} が壊れる`;
    } else {
      s = `${from} の構成が ${to} を参照している。${to} を消すと ${from} が壊れる`;
    }
    if (attrs.references !== undefined) s += `。参照は ${attrs.references} 件`;
    if (attrs.reason) s += ` — ${attrs.reason}`;
    if (e.suppressed) s = 'この線は存在しないと主張されている。' + s;
    return s;
  }

  // What a reachable or observed line permits, in the words the attributes
  // use: a protocol, and either one port or the range that was opened.
  function wire(attrs) {
    const parts = [];
    if (attrs.protocol) parts.push(String(attrs.protocol));
    const to = attrs.to_port !== undefined ? attrs.to_port : attrs.port;
    if (attrs.from_port !== undefined && to !== undefined && attrs.from_port !== to) {
      parts.push(`${attrs.from_port}-${to}`);
    } else if (to !== undefined) {
      parts.push(String(to));
    }
    return parts.join(' ');
  }

  // An end of a line is a node, a container, or something this graph does not
  // have — an edge may point at a box a filter took out of the drawing. A name
  // is broken over two lines to fit in a box; here it is read as a sentence.
  const endLabel = (id) => {
    const n = nodes.get(id) || allNodes().find((x) => x.id === id);
    const g = groups.get(id);
    const name = n ? (nameOf(n) || id) : (g && g.label ? g.label : id);
    return String(name).replace(/\s+/g, ' ').trim();
  };

  function focusGroup(id) {
    const selectedNodes = nodesUnderGroup(id);
    if (!selectedNodes.size) {
      selectGroup(id);
      return;
    }
    focus = id;
    focusNodes = selectedNodes;
    selectedGroup = id;
    selected = null;
    updateBreadcrumbs();
    render();
  }

  function clearFocus() {
    focus = null;
    focusNodes = null;
    updateBreadcrumbs();
    render();
  }

  /* ---- turning the page ------------------------------------------------
     An estate does not fit in one drawing, and the answer this page used to
     give — draw all of it, then let the reader fold what they did not want —
     asks the reader to build the picture they came for. An atlas is the
     other way round: a page is one level, one element or one call chain, and
     a box that has an inside opens it.

     Navigating is deliberately a swap of `graph` and nothing more clever.
     Every projection and every filter below reads whatever `graph` is, so a
     page turn costs a rebind and a repaint rather than a second renderer. */

  // Which diagram is on screen, in the URL, so that a reload and a shared
  // link both land where the reader was rather than back at the estate.
  function openDiagram(id, record = true) {
    const next = pages.get(id);
    if (!next) return;
    page = next;
    graph = next.graph;
    bindGraph();

    // The view state belongs to the page that was on screen. Carrying a fold
    // or a focus across a page turn would apply it to ids that mean something
    // else, and carrying the viewport would leave the reader looking at empty
    // canvas where the old diagram used to be.
    collapsed.clear();
    picked.clear();
    board.clearSelection();
    selected = null; selectedGroup = null; selectedEdge = null;
    focus = null; focusNodes = null;
    observationCutoff = 0;
    detail.hidden = true;
    fitted = false;

    if (record) {
      const url = new URL(location.href);
      if (id === atlas.root) url.searchParams.delete('at');
      else url.searchParams.set('at', id);
      history.pushState({diagram: id}, '', url);
    }

    // Every control built from the graph, because the graph changed. The
    // timeline is on this list for the same reason the filters are: it is
    // built from this page's observations, and one page's window over another
    // page's readings answers nothing.
    buildFilters();
    buildLabelFilters();
    buildTimeline();
    updateBreadcrumbs();
    render();
  }

  // The way back up. It is the containment chain rather than a history stack:
  // a reader who arrived at a pod three levels down wants the namespace it is
  // in, not the last page they happened to look at.
  function trail() {
    if (!page) return [];
    const out = [];
    for (let at = page; at; at = at.parent ? pages.get(at.parent) : null) {
      out.unshift(at);
      if (out.length > 32) break;   // a broken parent chain must not hang the page
    }
    return out;
  }

  // Where clicking this element goes, if anywhere.
  const openingFor = (id) => opens.get(id) || null;

  function updateBreadcrumbs() {
    const bar = document.getElementById('breadcrumbs');
    bar.textContent = '';

    if (atlas) {
      const pathOf = trail();
      pathOf.forEach((d, i) => {
        if (i) { const sep = document.createElement('span'); sep.textContent = '›'; bar.append(sep); }
        if (d === page) {
          const here = document.createElement('span');
          here.className = 'here';
          here.textContent = d.title;
          if (d.subtitle) here.title = d.subtitle;
          bar.append(here);
          return;
        }
        const up = document.createElement('button');
        up.textContent = d.title;
        up.addEventListener('click', () => openDiagram(d.id));
        bar.append(up);
      });
      if (page && page.kind) {
        const kind = document.createElement('span');
        kind.className = 'kind';
        kind.textContent = page.kind;
        bar.append(kind);
      }
      if (focus) {
        const sep = document.createElement('span'); sep.textContent = '·'; bar.append(sep);
        const back = document.createElement('button');
        back.textContent = 'この図の全体';
        back.addEventListener('click', clearFocus);
        bar.append(back);
      }
      return;
    }

    const all = document.createElement('button');
    all.textContent = 'all';
    all.addEventListener('click', clearFocus);
    bar.append(all);
    if (focus) {
      const sep = document.createElement('span'); sep.textContent = '›'; bar.append(sep);
      const current = document.createElement('span');
      const node = nodes.get(focus);
      const group = groups.get(focus);
      current.textContent = node ? (node.name || focus) : (group ? (group.label || focus) : focus);
      bar.append(current);
    }
  }

  const section = (title) => {
    const s = document.createElement('section');
    const h = document.createElement('h3');
    h.textContent = title;
    s.append(h);
    return s;
  };
  const text = (t) => { const d = document.createElement('div'); d.textContent = t; return d; };
  const withText = (s, t) => { s.append(text(t)); return s; };

  function defList(title, obj) {
    const s = section(title);
    const dl = document.createElement('dl');
    for (const k of Object.keys(obj).sort()) {
      const v = obj[k];
      if (v !== null && typeof v === 'object') continue;
      const dt = document.createElement('dt');
      dt.textContent = k;
      const dd = document.createElement('dd');
      dd.textContent = String(v);
      dl.append(dt, dd);
    }
    s.append(dl);
    return s;
  }

  /* ---- filters --------------------------------------------------------- */

  function buildFilters() {
    const counts = {};
    for (const n of graph.nodes) {
      const s = state(n) || 'none';
      counts[s] = (counts[s] || 0) + 1;
    }

    const bar = document.getElementById('filters');
    bar.textContent = '';
    for (const s of COVERAGE_ORDER) {
      if (!counts[s]) continue;
      const cov = COVERAGE[s];
      const chip = document.createElement('span');
      chip.className = 'chip on';
      chip.style.color = cov.stroke || '#3f9159';

      const dot = document.createElement('span');
      dot.className = 'dot';
      dot.style.background = cov.stroke || '#3f9159';

      const label = document.createElement('span');
      label.textContent = cov.label;

      const count = document.createElement('span');
      count.className = 'count';
      count.textContent = counts[s];

      chip.append(dot, label, count);
      chip.addEventListener('click', () => {
        if (hidden.has(s)) hidden.delete(s); else hidden.add(s);
        chip.classList.toggle('on', !hidden.has(s));
        chip.classList.toggle('off', hidden.has(s));
        render();
      });
      bar.append(chip);
    }
  }

  function buildTimeline() {
    const dates = (graph.observations || []).map((o) => o.observed_at).filter(Boolean)
      .sort((a, b) => Date.parse(a) - Date.parse(b));
    if (!dates.length) return;
    const bar = document.getElementById('filters');
    const label = document.createElement('label');
    label.className = 'timeline';
    label.textContent = ' observations through ';
    const input = document.createElement('input');
    input.type = 'datetime-local';
    input.title = 'Limit the observation details to this time';
    const localDateTime = (value) => {
      const d = new Date(value);
      const pad = (n) => String(n).padStart(2, '0');
      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
    };
    input.step = '1';
    input.min = localDateTime(dates[0]);
    input.max = localDateTime(dates[dates.length - 1]);
    input.value = input.max;
    observationCutoff = Date.parse(dates[dates.length - 1]);
    input.addEventListener('input', () => {
      observationCutoff = input.value ? Date.parse(input.value) : 0;
      // The cutoff changes what counts as abnormal, which is a colour on the
      // boxes and not only a line in the panel.
      render();
      if (selected) select(selected, false);
    });
    label.append(input);
    bar.append(label);
  }

  function buildLabelFilters() {
    const labels = new Set();
    for (const r of (graph.log_records || [])) for (const label of (r.labels || [])) labels.add(label);
    if (!labels.size) return;
    const bar = document.getElementById('filters');
    for (const label of [...labels].sort()) {
      const chip = document.createElement('span');
      chip.className = 'chip on';
      chip.textContent = '#' + label;
      chip.title = 'toggle log label ' + label;
      chip.addEventListener('click', () => {
        if (hiddenLabels.has(label)) hiddenLabels.delete(label); else hiddenLabels.add(label);
        chip.classList.toggle('on', !hiddenLabels.has(label));
        chip.classList.toggle('off', hiddenLabels.has(label));
        render();
      });
      bar.append(chip);
    }
  }

  function summary() {
    // A page that fell back to the plain drawing says so, and goes on saying
    // it. A reader who was handed a link to one diagram and got the whole
    // estate instead has no other way to tell that from the estate being what
    // was sent.
    if (atlasBroken) return atlasBroken;
    const counts = {};
    for (const n of graph.nodes) {
      const s = state(n);
      if (s) counts[s] = (counts[s] || 0) + 1;
    }
    const parts = COVERAGE_ORDER.filter((s) => counts[s]).map((s) => `${counts[s]} ${s}`);
    const window = (graph.metadata && graph.metadata.overlays || [])
      .map((o) => o.window).filter(Boolean)[0];
    let out = parts.join(' · ') + (window ? `   (${window})` : '');
    if (graph.log_status) {
      const s = graph.log_status;
      const poll = `logs: ${s.fetched} fetched / ${s.classified} classified`;
      out = out ? out + ' · ' + poll : poll;
      if (s.last_error) out += ` · last error: ${s.last_error}`;
    }
    return out;
  }

  /* ---- interaction ----------------------------------------------------- */

  function toggleGroup(id) {
    if (collapsed.has(id)) collapsed.delete(id); else collapsed.add(id);
    render();
  }

  const idOf = (cell) => (cell && cell.id ? String(cell.id).replace(/^(node|group):/, '') : null);
  // ⌘ on a Mac, Ctrl elsewhere — the same reading maxGraph has, kept here
  // because maxGraph has been told it has no toggle of its own.
  const togglesSelection = (event) =>
    !!event && (navigator.platform.startsWith('Mac') ? event.metaKey : event.ctrlKey);
  const kindOf = (cell) => (cell && cell.id && String(cell.id).startsWith('group:') ? 'group' : 'node');

  // Clicking the band along the top of a container folds it; clicking the rest
  // of it asks what the container is.
  //
  // The band is a shortcut and not the way in. On a dense diagram the lines
  // crossing a container cover its edge completely, and there is no point on
  // the band where the container itself is the topmost thing — measured on the
  // account diagram, where 179 lines cross one container. The control that
  // always works is the one in the detail panel. The title's own text is not a
  // target at all: text is only hit where a glyph is, and on a diagram scaled
  // to fit the title can be three pixels tall.
  const TITLE_BAND = 26;

  const inTitleBand = (cell, event) => {
    const state = board.getView().getState(cell);
    if (!state || !event) return false;
    const rect = board.container.getBoundingClientRect();
    const y = event.clientY - rect.top - state.y;
    return y >= 0 && y <= TITLE_BAND * board.getView().scale;
  };

  // Answering a click means consuming it. maxGraph selects the cell under the
  // pointer for any click left unconsumed, which would put back the single
  // selection this page had just replaced with several. It also clears the
  // selection for an unconsumed click on empty canvas, so that has to be done
  // here now rather than left to it.
  board.addListener(InternalEvent.CLICK, (sender, evt) => {
    if (dragged) { evt.consume(); return; }
    const cell = evt.getProperty('cell');
    if (!cell) {
      picked.clear();
      board.clearSelection();
      selected = null; selectedGroup = null; selectedEdge = null;
      detail.hidden = true;
      evt.consume();
      return;
    }
    if (cell.isEdge()) {
      if (cell.infra && cell.infra.edge) selectEdge(edgeKey(cell.infra.edge));
      evt.consume();
      return;
    }
    if (!cell.isVertex()) return;
    // ⌘ on a Mac, Ctrl elsewhere — maxGraph's own reading of "and this one
    // too". Shift is not free: it is the drag that asserts a line. Picking
    // several is only offered while editing, because moving them is.
    if (kindOf(cell) !== 'group') {
      select(idOf(cell), false, editing && togglesSelection(evt.getProperty('event')));
      evt.consume();
      return;
    }
    if (inTitleBand(cell, evt.getProperty('event'))) toggleGroup(idOf(cell));
    else selectGroup(idOf(cell));
    evt.consume();
  });

  board.addListener(InternalEvent.DOUBLE_CLICK, (sender, evt) => {
    const cell = evt.getProperty('cell');
    if (!cell || !cell.isVertex()) return;
    evt.consume();
    const id = idOf(cell);
    // A box that has an inside opens it. This comes before every other
    // reading of a second click, because it is the one the reader means: the
    // question that brought them to a container is what is in it.
    const open = openingFor(id);
    if (open && !editing) { openDiagram(open.diagram); return; }
    if (kindOf(cell) === 'group') { focusGroup(id); return; }
    // In edit mode a second click on a box that was placed by hand releases it
    // back to the layout. Reading, it always means "show me what is around
    // this".
    if (editing && positions.delete(id)) { syncTools(); render(); return; }
    focusOn(id);
  });

  // A placement is a fact about this session, not a change to the graph, so it
  // goes into `positions` — the layout document — and nothing else. maxGraph
  // has already moved the box and re-drawn the lines by the time this runs.
  board.addListener(InternalEvent.CELLS_MOVED, (sender, evt) => {
    const dx = evt.getProperty('dx') || 0;
    const dy = evt.getProperty('dy') || 0;
    const moved = [];
    for (const cell of evt.getProperty('cells') || []) {
      if (!cell.isVertex() || kindOf(cell) === 'group') continue;
      const geo = cell.getGeometry();
      const parent = cell.getParent();
      const id = idOf(cell);
      positions.set(id, {
        id,
        parent: parent && String(parent.id || '').startsWith('group:') ? idOf(parent) : undefined,
        x: geo.x, y: geo.y,
      });
      // The container did not move, so the box moved by the same amount in
      // both coordinate systems.
      const rect = absRect.get(id);
      if (rect) absRect.set(id, {...rect, x: rect.x + dx, y: rect.y + dy});
      moved.push(id);
      // Every line on the box that moved is re-anchored, and so is every line
      // sharing a side with one of them: a lane that gains or loses a member
      // spaces the rest differently.
    }
    if (moved.length) applyAnchors();
    syncTools();
  });

  // A box that was resized keeps its size, and its lines are re-anchored: the
  // sides they meet are fractions of a box that is no longer that shape.
  board.addListener(InternalEvent.CELLS_RESIZED, (sender, evt) => {
    for (const cell of evt.getProperty('cells') || []) {
      if (!cell.isVertex() || kindOf(cell) === 'group') continue;
      const geo = cell.getGeometry();
      const id = idOf(cell);
      sizes.set(id, {width: Math.round(geo.width), height: Math.round(geo.height)});
      // The schema names a box by where it is, so a box that was only resized
      // still records the place it is already in.
      const parent = cell.getParent();
      positions.set(id, {
        id,
        parent: parent && String(parent.id || '').startsWith('group:') ? idOf(parent) : undefined,
        x: geo.x, y: geo.y,
      });
      const rect = absRect.get(id);
      if (rect) absRect.set(id, {...rect, w: geo.width, h: geo.height});
    }
    applyAnchors();
    syncTools();
  });

  // Shift-drag between two boxes asserts a connection. It is handled here
  // rather than through maxGraph's connection handler because that one offers
  // a handle on every box and lets go of it into empty space to create a node;
  // this page has one way to add a box and it is a button, and a line drawn
  // here is a claim about two things that already exist.
  let asserting = null;
  // Every gesture ends in a mouse-up, and maxGraph reports a click for it
  // whether the pointer travelled or not. Answering the click that ends a drag
  // undoes what the drag did: a move loses every box but the one under the
  // pointer, and a box drawn round several ends on empty canvas, which reads
  // as "clicked nothing" and clears the lot.
  let dragged = false;
  let pressedAt = null;

  board.addMouseListener({
    mouseDown(sender, me) {
      dragged = false;
      pressedAt = {x: me.getX(), y: me.getY()};
      if (!editing || !me.getEvent().shiftKey) return;
      const cell = me.getCell();
      if (!cell || !cell.isVertex() || kindOf(cell) === 'group') return;
      asserting = idOf(cell);
      me.consume();
    },
    mouseMove(sender, me) {
      // Far enough to be a drag rather than an unsteady hand.
      if (pressedAt && Math.hypot(me.getX() - pressedAt.x, me.getY() - pressedAt.y) > 3) dragged = true;
      if (asserting) me.consume();
    },
    mouseUp(sender, me) {
      if (!asserting) return;
      const cell = me.getCell();
      const to = cell && cell.isVertex() && kindOf(cell) === 'node' ? idOf(cell) : null;
      if (to && to !== asserting) assert(asserting, to);
      asserting = null;
      me.consume();
    },
  });

  // The gesture has to be offered the press before maxGraph's own handlers.
  // They registered themselves while the graph was being built and
  // addMouseListener appends, so this one is last, and the selection handler
  // — which skips an event that has already been consumed — would otherwise
  // read the shift-drag as picking the box up and moving it.
  board.mouseListeners.unshift(board.mouseListeners.pop());

  // How far out the wheel may go. It starts at a floor that keeps a small
  // graph from being scrolled into a speck, and fit lowers it to whatever fit
  // itself chose — because on a canvas metres wide fit lands below that floor,
  // and a fixed floor would answer the first "show me more" by snapping the
  // view *closer*. Turning the wheel back would not undo it, and there is no
  // reset control: the reader would be stuck, on exactly the graph this fit
  // was written for.
  // A wheel moves the view; a pinch scales it. Two fingers on a trackpad send
  // a wheel, and a pinch sends one too but with ctrlKey set — that flag is the
  // only thing telling the two apart, and it is what every drawing tool reads.
  // Zooming on every wheel, which is what this did, took the whole of a
  // trackpad away: two fingers scaled the diagram instead of moving it, and
  // there was no way left to scroll.
  canvas.addEventListener('wheel', (e) => {
    e.preventDefault();
    const view = board.getView();
    if (!e.ctrlKey && !e.metaKey) {
      // deltaX/deltaY are pixels on the screen; the translate is graph units.
      view.scaleAndTranslate(view.scale,
        view.translate.x - e.deltaX / view.scale,
        view.translate.y - e.deltaY / view.scale);
      return;
    }

    const rect = canvas.getBoundingClientRect();
    const mx = e.clientX - rect.left, my = e.clientY - rect.top;
    // A pinch reports a much smaller delta than a wheel notch, so the step is
    // taken from how far it actually moved rather than from its direction.
    const k = Math.min(4, Math.max(minK, view.scale * Math.exp(-e.deltaY / 400)));

    // The translate is in graph units, so the pixel the pointer is over is
    // held still by converting it through both scales.
    const px = view.translate.x * view.scale, py = view.translate.y * view.scale;
    view.scaleAndTranslate(k, (mx - (mx - px) * (k / view.scale)) / k,
      (my - (my - py) * (k / view.scale)) / k);
  }, {passive: false});

  // fit sizes the view to the laid-out graph, with room around it so the
  // outermost boxes are not flush against the edge. It reports whether it
  // could: a canvas that has not been laid out yet, or a root ELK sized at
  // zero, gives nothing to fit to, and the caller needs to know that it should
  // try again rather than record a fit that never happened.
  function fit(layout) {
    const cw = canvas.clientWidth;
    const ch = canvas.clientHeight;
    const w = layout.width || 0;
    const h = layout.height || 0;
    if (!cw || !ch || !w || !h) return false;

    // Room around the graph, but never more room than there is canvas: a
    // window shorter than the padding would otherwise leave a negative amount
    // of space, and a negative scale draws the whole diagram mirrored.
    const pad = 24;
    const availW = Math.max(1, cw - pad * 2);
    const availH = Math.max(1, ch - pad * 2);

    // Never magnify: a graph of three boxes blown up to fill a screen looks
    // like a mistake rather than a diagram.
    const k = Math.min(1, availW / w, availH / h);
    board.getView().scaleAndTranslate(k, (cw - w * k) / 2 / k, (ch - h * k) / 2 / k);
    minK = Math.min(minK, k);
    return true;
  }

  /* ---- authoring -------------------------------------------------------
     A line drawn here is an assertion, and an assertion is a file. Nothing is
     written into the page's own graph as though it had been found there: the
     export is the artifact, and re-running the CLI with it is what makes the
     claim real. */

  function assert(from, to) {
    const kind = window.prompt(
      `Assert a connection\n\n  ${from}\n  → ${to}\n\n` +
      `Edge kind: observed, reachable or iac_ref.\n` +
      `Leave empty to cancel.`, 'observed');
    if (!kind) return;
    if (!['observed', 'reachable', 'iac_ref'].includes(kind)) {
      window.alert(`"${kind}" is not an edge kind. Use observed, reachable or iac_ref.`);
      return;
    }
    const note = window.prompt('Why do you believe this? (optional)', '') || '';

    const claim = {origin: 'human', note};
    pending.push({
      assertion: {
        assert: 'edge',
        from: {node: from},
        to: {node: to},
        kind,
        ...(note ? {note} : {}),
      },
      edge: {from, to, kind, claim},
    });

    syncTools();
    render();
  }

  // Renaming is authoring, so it produces an assertion like every other edit
  // here. The field sits in the detail panel rather than on the box: a
  // double-click already means drill-down, and a name typed into the diagram
  // would have to fight the layout for the space to type it in.
  function renameControl(n) {
    const s = section('name');
    const form = document.createElement('form');
    form.className = 'rename';

    // A textarea, not a text input, because a name may contain newlines. This
    // viewer draws such a name on one line, but the SVG and Mermaid renderers
    // break it, and estates use that to put an account number under the
    // account's name. A text input drops the character on the way in, so
    // opening the field to fix a typo would quietly rewrite the name
    // everywhere else it is drawn.
    const current = nameOf(n) || '';
    const input = document.createElement('textarea');
    input.value = current;
    input.rows = Math.min(4, current.split('\n').length);
    input.setAttribute('aria-label', 'display name');
    const go = document.createElement('button');
    go.type = 'submit';
    go.textContent = 'Rename';
    form.append(input, go);

    // Enter is a newline in a textarea, so it cannot also be the commit.
    // Ctrl/Cmd+Enter is, for the hands already in the field.
    input.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter' && (ev.metaKey || ev.ctrlKey)) {
        ev.preventDefault();
        form.requestSubmit();
      }
    });
    form.addEventListener('submit', (ev) => {
      ev.preventDefault();
      const name = input.value.trim();
      if (!name || name === nameOf(n)) return;
      rename(n.id, name);
    });
    s.append(form);
    return s;
  }

  function rename(id, name) {
    // A type is not restated. The input already said what this is, and saying
    // it again would make a human the claimant of something nobody disputed.
    pending.push({assertion: {assert: 'node', subject: {node: id}, name}});
    renamed.set(id, name);
    syncTools();
    render();
    select(id);
  }

  // A box added here is the same claim the CLI makes when an overlay names
  // something no parser produced: it adopts it. The id is built the way the
  // CLI builds it for this selector, so re-applying the export lands on the
  // same node rather than a second one beside it.
  function addNode() {
    const typed = window.prompt(
      'Add a box\n\nIt asserts that something exists which is in no input file.\n' +
      'Leave empty to cancel.', '');
    if (!typed) return;
    const name = typed.trim();
    if (!name) return;

    const id = 'asserted:name=' + name;
    if (nodes.has(id)) { flash('already drawn'); select(id); return; }

    const node = {id, type: 'oekaki_asserted', name, provider: 'oekaki', groups: {},
      claim: {origin: 'human', note: 'drawn in the interactive view'}};
    pending.push({assertion: {assert: 'node', subject: {name}, name}, node});
    nodes.set(id, node);
    syncTools();
    render();
    select(id);
  }

  function overlayDocument() {
    return JSON.stringify({
      kind: 'oekaki.overlay',
      version: '0.1',
      metadata: {origin: 'human', note: 'drawn in the interactive view'},
      assertions: pending.map((p) => p.assertion),
    }, null, 2) + '\n';
  }

  // asDrawn is every box where it is on screen right now, whether a hand put
  // it there or the engine did. A hand's answer wins where there is one: it
  // was chosen, and the engine's was only computed.
  //
  // It is what reading a diagram has to be able to hand over. The alternative
  // — the hand-moved boxes alone — is empty for somebody who has not moved
  // anything, which is everybody who is only reading, and a document with no
  // positions in it places nothing.
  function asDrawn() {
    const out = new Map(computed);
    for (const [id, at] of positions) out.set(id, at);
    return out;
  }

  function layoutDocument(from) {
    // Written in the words the IR uses for a line rather than the encoded name
    // this page indexes it under: the document is meant to be read, and edited,
    // by whoever exported it.
    const edges = [...edgeAnchors.values()].map((a) => ({
      from: a.from, to: a.to, kind: a.kind,
      ...(a.relation ? {relation: a.relation} : {}),
      ...(a.source ? {source: a.source} : {}),
      ...(a.target ? {target: a.target} : {}),
      ...(a.line ? {line: a.line} : {}),
    })).sort((a, b) => edgeKey(a).localeCompare(edgeKey(b)));

    return JSON.stringify({kind: 'oekaki.layout', version: '0.2',
      nodes: [...(from || positions).values()].map((n) => {
        const size = sizes.get(n.id);
        // Rounded, because a position is a pixel and the sixteen digits the
        // engine hands back are not sixteen digits of anything. The document
        // says it is meant to be read and edited by whoever exported it, and
        // it now carries every box rather than the two or three somebody
        // moved — so the difference between 1284.5553165584414 and 1285 is
        // the difference between a file a person can work in and one they
        // cannot. It also makes the round trip stable: what is exported and
        // read back exports again unchanged.
        const at = {...n, x: Math.round(n.x), y: Math.round(n.y)};
        return size ? {...at, ...(size.width ? {width: size.width} : {}),
          ...(size.height ? {height: size.height} : {})} : at;
      }).sort((a, b) => a.id.localeCompare(b.id)),
      ...(lineShape === 'curved' ? {} : {lines: lineShape}),
      ...(edges.length ? {edges} : {}),
      claim: {origin: 'human'}}, null, 2) + '\n';
  }

  async function saveDocument(text, name) {
    if (window.showSaveFilePicker) {
      try {
        const handle = await window.showSaveFilePicker({suggestedName: name,
          types: [{description: 'oekaki JSON', accept: {'application/json': ['.json']}}]});
        const w = await handle.createWritable(); await w.write(text); await w.close(); return;
      } catch (err) { if (err && err.name === 'AbortError') return; }
    }
    const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([text], {type: 'application/json'}));
    a.download = name; a.click(); URL.revokeObjectURL(a.href);
  }

  let layoutExport = document.getElementById('export-layout');
  if (!layoutExport) {
    layoutExport = document.createElement('button');
    layoutExport.id = 'export-layout'; layoutExport.hidden = true; layoutExport.textContent = 'Export layout';
    document.getElementById('bar').insertBefore(layoutExport, document.getElementById('copy'));
  }
  // A server that handed this page out can take the layout straight back. That
  // is the whole difference between a round trip and a downloads folder: the
  // file goes where the page came from, under the name it was opened as, and
  // nobody has to remember a path.
  //
  // These are not const because a save under no particular name gets given
  // one, and from then on this page is that version — see savedAs.
  let layoutPost = document.body.dataset.layoutPost;
  let overlayPost = document.body.dataset.overlayPost;
  const defaultPost = document.body.dataset.defaultPost;
  if (layoutPost) layoutExport.textContent = 'Save';

  // The name the page is saving under. Empty until a save happens on a page
  // that was opened without one.
  let savedAs = new URLSearchParams(location.search).get('layout') || '';

  async function post(url, text) {
    const r = await fetch(url, {method: 'POST',
      headers: {'Content-Type': 'application/json'}, body: text});
    if (!r.ok) throw new Error(await r.text());
    return {said: await r.text(), name: r.headers.get('Oekaki-Name') || ''};
  }

  // savedUnder is what makes a save survive the reload after it.
  //
  // Saving without a name gets one made up from the clock at the other end,
  // and until the page is told what it was, the version it just wrote is
  // unreachable: the url it would reload under does not name it, and nothing
  // on screen says what to type. The work was on disk and gone at the same
  // time, which is the worst of both.
  //
  // So the name goes into the url, and into where the next save is sent —
  // otherwise every press leaves another minute-stamped copy behind and the
  // one before it is the one that is still promoted.
  function savedUnder(name) {
    if (!name || name === savedAs) return;
    savedAs = name;
    layoutPost = layoutPost.replace(/\/[^/]*$/, '/' + encodeURIComponent(name));
    // Only an overlay that was not opened under a name of its own follows the
    // layout's. One that was is somebody else's document, and moving it would
    // leave whatever pointed at it pointing at nothing.
    if (overlayPost && overlayPost.endsWith('/')) overlayPost += encodeURIComponent(name);
    const url = new URL(location.href);
    url.searchParams.set('layout', name);
    history.replaceState(null, '', url);
    syncTools();
  }

  layoutExport.addEventListener('click', async () => {
    // Reading, the button hands over a file and asks the server for nothing.
    // Editing is a mode, not a modifier: a page being read does not write.
    if (!layoutPost || !editing) { saveDocument(layoutDocument(asDrawn()), 'layout.json'); return; }
    // A box drawn here has two facts about it: that it exists, which only an
    // overlay carries, and where it sits, which only a layout carries. Saving
    // one without the other loses half of it, and the half that goes is the
    // box itself. So one press saves both.
    try {
      // The whole picture, not only the boxes a hand moved. What comes back
      // on the next reload is then what was on screen when the button was
      // pressed — including an arrangement the engine chose, which nothing
      // else records and which would otherwise be gone.
      const first = await post(layoutPost, layoutDocument(asDrawn()));
      const said = [first.said];
      // Named before the overlay goes, so both halves are filed under the one
      // name. Opening a layout whose overlay answers to something else brings
      // back positions for boxes that are not there.
      savedUnder(first.name);
      if (pending.length > 0 && overlayPost) {
        said.push((await post(overlayPost, overlayDocument())).said);
      }
      flash(said.join(', '));
    } catch (err) { flash('not saved: ' + err.message); }
  });

  // Making a saved version the one everybody gets is its own press, and not
  // part of saving.
  //
  // Saving is private: it writes a file nobody else is looking at, and the
  // journal deliberately does not record it. Promoting changes what this page
  // draws for the next person who opens it, and is recorded. Folding the
  // second into the first would mean every stray press of Save rewrote the
  // diagram out from under whoever else had it open, and there would be no
  // way left to save without doing that.
  const makeDefault = document.getElementById('make-default');
  if (makeDefault) {
    makeDefault.addEventListener('click', async () => {
      if (!defaultPost || !savedAs) return;
      try {
        const r = await fetch(defaultPost + encodeURIComponent(savedAs), {method: 'POST'});
        const said = await r.text();
        if (!r.ok) throw new Error(said);
        flash(said);
      } catch (err) { flash('not set: ' + err.message); }
    });
  }

  /* ---- mode ------------------------------------------------------------
     Read mode hides the controls that author something rather than merely
     refusing the gestures. A button that offers to export nothing is the part
     that made the old page ambiguous: a plain click on a box counted as a
     placement and put "Export layout" on screen, so the page looked as though
     something had been authored when nothing had.

     Handing over the layout is no longer one of those. It says where the
     boxes are, which is true of a diagram nobody has touched, so it is
     offered in both modes — and while reading it writes a file rather than
     posting one, because reading does not write. */

  const readButton = document.getElementById('mode-read');
  const editButton = document.getElementById('mode-edit');
  const addButton = document.getElementById('add-node');
  addButton.addEventListener('click', addNode);
  const tools = document.getElementById('tools');

  // Lining several boxes up by hand is arithmetic, and arithmetic is what a
  // drawing tool is for. Each of these moves the picked boxes and nothing
  // else, records where it put them, and re-routes what it touched — no
  // layout runs, so the rest of the diagram does not shift underneath.
  function pickedBoxes() {
    return [...picked].map((id) => cells.get(id)).filter(Boolean);
  }

  function arrange(place) {
    const boxes = pickedBoxes();
    if (boxes.length < 2) return;
    const rects = boxes.map((cell) => ({cell, geo: cell.getGeometry().clone(),
      abs: absRect.get(idOf(cell))}));
    board.batchUpdate(() => {
      for (const {cell, geo, moved} of place(rects)) {
        board.getDataModel().setGeometry(cell, geo);
        const id = idOf(cell);
        const parent = cell.getParent();
        positions.set(id, {
          id,
          parent: parent && String(parent.id || '').startsWith('group:') ? idOf(parent) : undefined,
          x: geo.x, y: geo.y,
        });
        const rect = absRect.get(id);
        if (rect) absRect.set(id, {...rect, x: rect.x + moved.x, y: rect.y + moved.y});
      }
    });
    applyAnchors();
    syncTools();
  }

  // An edge to line up on, or a middle. Taken from the boxes as they are, so
  // the arrangement does not depend on which one happens to be first.
  const edgeOf = (rects, pick) => pick(rects.map(({geo}) => geo));
  const moveTo = (rects, to) => rects.map(({cell, geo}) => {
    const was = {x: geo.x, y: geo.y};
    to(geo);
    return {cell, geo, moved: {x: geo.x - was.x, y: geo.y - was.y}};
  });

  const alignments = {
    'align-left':   (r) => { const v = edgeOf(r, (g) => Math.min(...g.map((q) => q.x))); return moveTo(r, (g) => { g.x = v; }); },
    'align-right':  (r) => { const v = edgeOf(r, (g) => Math.max(...g.map((q) => q.x + q.width))); return moveTo(r, (g) => { g.x = v - g.width; }); },
    'align-centre': (r) => { const v = edgeOf(r, (g) => g.reduce((s, q) => s + q.x + q.width / 2, 0) / g.length); return moveTo(r, (g) => { g.x = v - g.width / 2; }); },
    'align-top':    (r) => { const v = edgeOf(r, (g) => Math.min(...g.map((q) => q.y))); return moveTo(r, (g) => { g.y = v; }); },
    'align-bottom': (r) => { const v = edgeOf(r, (g) => Math.max(...g.map((q) => q.y + q.height))); return moveTo(r, (g) => { g.y = v - g.height; }); },
    'align-middle': (r) => { const v = edgeOf(r, (g) => g.reduce((s, q) => s + q.y + q.height / 2, 0) / g.length); return moveTo(r, (g) => { g.y = v - g.height / 2; }); },
    'spread-x': (r) => spread(r, 'x', 'width'),
    'spread-y': (r) => spread(r, 'y', 'height'),
  };

  // Even gaps between the outermost two, in the order the boxes already sit
  // in: evening the gaps is not a reason to reorder them.
  function spread(rects, axis, size) {
    if (rects.length < 3) return [];
    const order = [...rects].sort((a, b) => a.geo[axis] - b.geo[axis]);
    const first = order[0].geo[axis];
    const last = order[order.length - 1].geo[axis] + order[order.length - 1].geo[size];
    const used = order.reduce((s, {geo}) => s + geo[size], 0);
    const gap = (last - first - used) / (order.length - 1);
    let at = first;
    return order.map(({cell, geo}) => {
      const was = geo[axis];
      geo[axis] = at;
      at += geo[size] + gap;
      return {cell, geo, moved: axis === 'x' ? {x: geo.x - was, y: 0} : {x: 0, y: geo.y - was}};
    });
  }

  for (const [id, place] of Object.entries(alignments)) {
    document.getElementById(id).addEventListener('click', () => arrange(place));
  }
  document.getElementById('select-all').addEventListener('click', () => {
    picked.clear();
    for (const [id, cell] of cells) if (kindOf(cell) === 'node') picked.add(id);
    if (picked.size) { selected = [...picked][picked.size - 1]; describePicked(); }
    syncTools();
  });
  document.getElementById('select-none').addEventListener('click', () => {
    picked.clear();
    board.clearSelection();
    selected = null;
    detail.hidden = true;
    syncTools();
  });
  document.getElementById('fit').addEventListener('click', () => {
    fitted = false;
    render();
  });

  // Curves or right angles, for the whole diagram. It is a switch rather than
  // a per-line chore: a dense page is not made orthogonal one line at a
  // time. It sits with the authoring controls because it is authored — it is
  // written into the layout document and read back with --layout.
  let shapeSwitch = document.createElement('div');
  const shapeButtons = [['curved', '曲線'], ['orthogonal', 'カクカク']].map(([shape, text]) => {
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = text;
    button.addEventListener('click', () => setLineShape(shape));
    shapeSwitch.append(button);
    return {shape, button};
  });
  tools.firstElementChild.append(...shapeSwitch.children);
  shapeSwitch = tools.firstElementChild;

  function syncTools() {
    const claims = pending.length > 0;
    tools.hidden = !editing;
    // Lining boxes up needs boxes; evening the gaps needs a gap to even.
    for (const id of Object.keys(alignments)) {
      const need = id.startsWith('spread') ? 3 : 2;
      document.getElementById(id).disabled = picked.size < need;
    }
    for (const {shape, button} of shapeButtons) {
      button.setAttribute('aria-pressed', String(shape === lineShape));
    }
    document.getElementById('export').hidden = !editing || !claims;
    document.getElementById('copy').hidden = !editing || !claims;
    // Offered as soon as there is a drawing to hand over, in either mode.
    //
    // It used to be hidden while reading, and the reason was sound at the
    // time: the document was the boxes a hand had moved, so reading — where
    // nothing has been moved — could only ever have exported a document that
    // placed nothing, and a button offering that is worse than no button. Now
    // it exports what is on screen, which is exactly what somebody reading a
    // diagram and wanting its layout is asking for.
    layoutExport.hidden = computed.size === 0 && positions.size === 0;
    // Making a version the one everybody gets needs a version. Until this
    // page has saved one there is nothing to point at, and a server that
    // takes no decisions has nowhere to send it.
    const makeDefaultButton = document.getElementById('make-default');
    if (makeDefaultButton) makeDefaultButton.hidden = !editing || !defaultPost || !savedAs;
  }

  function setMode(next) {
    editing = next;
    // Reading, a box cannot be picked up at all. The mode is not a coat of
    // paint over the same canvas: it decides whether a drag moves the diagram
    // or the thing under the pointer.
    board.setCellsMovable(editing);
    board.setCellsResizable(editing);
    board.setCellsSelectable(true);
    rubberBand.setEnabled(editing);
    document.body.dataset.mode = editing ? 'edit' : 'read';
    if (selected) select(selected);
    readButton.setAttribute('aria-pressed', String(!editing));
    editButton.setAttribute('aria-pressed', String(editing));
    // The mode belongs in the URL: a reload in the middle of an edit should
    // not drop back to reading, and a link should be able to hand somebody
    // the page in the state it was worked in.
    const url = new URL(location.href);
    if (editing) url.searchParams.set('edit', '1'); else url.searchParams.delete('edit');
    history.replaceState(null, '', url);
    syncTools();
  }

  readButton.addEventListener('click', () => setMode(false));
  editButton.addEventListener('click', () => setMode(true));
  setMode(editing);

  document.getElementById('export').addEventListener('click', async () => {
    const text = overlayDocument();

    // The File System Access API writes in place, which closes the loop: the
    // same file can be overwritten and fed straight back to the CLI. It is
    // Chromium-only, so the download below is the path everyone else takes.
    if (window.showSaveFilePicker) {
      try {
        const handle = await window.showSaveFilePicker({
          suggestedName: 'overlay.json',
          types: [{description: 'oekaki overlay', accept: {'application/json': ['.json']}}],
        });
        const w = await handle.createWritable();
        await w.write(text);
        await w.close();
        return;
      } catch (err) {
        if (err && err.name === 'AbortError') return;
        // Anything else falls through to the download, which always works.
      }
    }

    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text], {type: 'application/json'}));
    a.download = 'overlay.json';
    a.click();
    URL.revokeObjectURL(a.href);
  });

  document.getElementById('copy').addEventListener('click', async () => {
    const text = overlayDocument();
    try {
      await navigator.clipboard.writeText(text);
      flash('copied');
      return;
    } catch (err) {
      // Clipboard access is refused in plenty of contexts. A textarea the
      // user can select from is the fallback that has always worked.
    }
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;inset:10% 20%;z-index:9;width:60%;height:70%';
    document.body.append(ta);
    ta.select();
  });

  function flash(msg) {
    const s = document.getElementById('status');
    const was = s.textContent;
    s.textContent = msg;
    setTimeout(() => { s.textContent = was; }, 1200);
  }

  /* ---- layout ---------------------------------------------------------- */

  const elk = new ELK();
  let generation = 0;

  // render is for the changes that move things: folding a container, a filter
  // that takes boxes out of the picture, a box or a line added to the pending
  // set, a rename wide enough to change the size of its box.
  //
  // Selecting, deselecting and highlighting do not come here. They move
  // nothing, and sending them through a layout is what used to make a click
  // that changed one highlight cost a substantial fraction of a second on a
  // large diagram. maxGraph redraws the cells that changed; that is the
  // whole of what those gestures now cost.
  async function render() {
    const mine = ++generation;
    try {
      const laid = await elk.layout(buildRoot());
      if (mine !== generation) return;   // a newer render started while we waited
      paint(laid);
    } catch (err) {
      // A blank canvas is the worst failure this tool has: an estate with
      // nothing in it looks exactly like a layout that threw. Say which it is.
      if (mine !== generation) return;
      fail('layout failed: ' + (err && err.message ? err.message : String(err)));
      throw err;
    }
  }

  /* ---- scoring a layout -------------------------------------------------
     What makes one of these pictures hard to read is measurable, and until
     now nothing measured it: the engine was asked for an answer with one set
     of options and whatever came back was the diagram.

     Four numbers, because they are the four complaints. A line that turns
     six times is followed by nobody. Lines crossing each other is the thing
     that turns a diagram into a hairball. Long lines are travel with nothing
     said along the way. And a drawing taller than the screen is read a
     third at a time.

     This is not a layout engine and must not become one — ELK is better at
     that than this project will ever be. It is a way to ask ELK several
     times and keep the answer that reads best. */

  // segments walks the laid-out tree and yields every drawn line in one
  // coordinate system.
  //
  // ELK writes an edge's route relative to whatever container it attached the
  // edge to, which is not the container either endpoint sits in — the same
  // offset placeChildren carries for the same reason. Measuring without
  // applying it compares lines that were never drawn together.
  function segments(laid) {
    const out = [];
    const walk = (container, ox, oy) => {
      for (const e of container.edges || []) {
        for (const s of e.sections || []) {
          const points = [s.startPoint, ...(s.bendPoints || []), s.endPoint]
            .filter(Boolean).map((p) => ({x: ox + p.x, y: oy + p.y}));
          for (let i = 1; i < points.length; i++) out.push([points[i - 1], points[i]]);
        }
      }
      for (const c of container.children || []) {
        walk(c, ox + (c.x || 0), oy + (c.y || 0));
      }
    };
    walk(laid, 0, 0);
    return out;
  }

  // How much work a line does on its way across.
  //
  // Drawn with right angles these are corners, and the count is literally how
  // many times the line turns. Drawn as curves they are the control points of
  // the spline, which is a larger number for the same picture — a wigglier
  // route needs more of them, so it still ranks routes against each other,
  // but the two shapes are not on the same scale. Nothing here compares
  // across a change of shape: every candidate is laid out with whichever one
  // the page is currently drawing.
  const bendCount = (laid) => {
    let n = 0;
    const walk = (c) => {
      for (const e of c.edges || []) for (const s of e.sections || []) n += (s.bendPoints || []).length;
      (c.children || []).forEach(walk);
    };
    walk(laid);
    return n;
  };

  // Proper crossings only: two segments that share an endpoint meet at a box
  // or at a corner of one line, and neither is the thing that makes a diagram
  // unreadable. Counting them would score a fan of lines leaving one box as
  // badly as a knot.
  function crossingCount(segs) {
    const side = (a, b, c) => Math.sign((b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x));
    let n = 0;
    for (let i = 0; i < segs.length; i++) {
      const [p1, p2] = segs[i];
      for (let j = i + 1; j < segs.length; j++) {
        const [p3, p4] = segs[j];
        const d1 = side(p3, p4, p1), d2 = side(p3, p4, p2);
        const d3 = side(p1, p2, p3), d4 = side(p1, p2, p4);
        if (d1 !== d2 && d3 !== d4 && d1 && d2 && d3 && d4) n++;
      }
    }
    return n;
  }

  function measureLayout(laid) {
    const segs = segments(laid);
    let length = 0;
    for (const [a, b] of segs) length += Math.hypot(b.x - a.x, b.y - a.y);
    return {
      bends: bendCount(laid),
      crossings: crossingCount(segs),
      length: Math.round(length),
      // The size across the flow: what "too tall" means laid out left to
      // right, and what "too wide" means laid out top to bottom.
      across: Math.round(layoutDirection === 'RIGHT' ? (laid.height || 0) : (laid.width || 0)),
    };
  }

  // Weighted against the first candidate rather than in raw units, because
  // the four are counted in different things — a bend is not comparable to a
  // pixel, and summing them unweighted would let the one with the largest
  // numbers decide alone. The three that were asked for carry about the same
  // weight; total line length is a tie-breaker between layouts that are
  // otherwise level, not a complaint in its own right.
  const WEIGHTS = {bends: 1, crossings: 1.2, across: 1, length: 0.3};

  function scoreAgainst(base, m) {
    let total = 0;
    for (const [key, weight] of Object.entries(WEIGHTS)) {
      // A baseline of zero is already perfect on that count, so there is no
      // ratio to take and nothing to improve; anything worse still shows up
      // in the other three.
      total += base[key] ? weight * (m[key] / base[key]) : 0;
    }
    return total;
  }

  /* ---- laying out again --------------------------------------------------
     ELK was being asked one way. It has options that change exactly the
     things people complain about, and which one wins depends on the graph —
     a diagram of thirty boxes and one of two hundred do not want the same
     answer, and neither is knowable from here. So try them and measure. */

  // Every one of these was measured against a graph dense enough for the
  // differences to show, and kept only because it moved the numbers. That
  // check is worth repeating before adding to the list, because most of the
  // obvious knobs do nothing: BRANDES_KOEPF with BALANCED alignment,
  // LAYER_SWEEP crossing minimization and a raised thoroughness are what ELK
  // already does, and asking for them by name returns a layout identical to
  // the first, at the cost of a second run. Others are worse on every count,
  // and one — MIN_WIDTH layering — took seconds to produce the worst picture
  // of the lot.
  //
  // The tighter spacing appears in most of them because the complaint it
  // answers is size, and spacing is what size is made of.
  const TIGHT = {'elk.spacing.nodeNode': '14', 'elk.layered.spacing.nodeNodeBetweenLayers': '60'};
  const CANDIDATES = [
    {name: 'as generated', options: {}},
    {name: 'tight', options: TIGHT},
    {name: 'straight', options: {...TIGHT, 'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX'}},
    {name: 'aligned', options: {...TIGHT, 'elk.layered.nodePlacement.bk.fixedAlignment': 'LEFTUP'}},
    {name: 'segments', options: {...TIGHT, 'elk.layered.nodePlacement.strategy': 'LINEAR_SEGMENTS'}},
    {name: 'roomy', options: {
      'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX',
      'elk.spacing.edgeEdge': '8',
      'elk.spacing.edgeNode': '12',
    }},
  ];

  let tidying = false;

  // tidy lays the diagram out several ways and keeps the one that measures
  // best, then writes where every box landed into the layout document.
  //
  // Positions chosen by hand are dropped first, on purpose. Half of them
  // pinned and the rest laid out around them is neither picture: the engine
  // would be solving a problem nobody posed, and the result reads worse than
  // either. Somebody who wants their placements kept does not press this.
  //
  // Dropping them is also what keeps the routes. A box this page holds a
  // position for is a box the engine's route was drawn for and no longer
  // fits, so every line touching one is re-drawn here instead — by a router
  // that does not avoid boxes. Writing the engine's own answer back into
  // those positions would throw away the routing this just spent six layouts
  // choosing. The result is kept in `computed`, which is what the layout
  // document is made of, and the placements stay empty.
  async function tidy() {
    if (tidying) return;
    tidying = true;
    const button = document.getElementById('tidy');
    button.disabled = true;
    const status = document.getElementById('status');
    const was = status.textContent;
    try {
      let best = null, base = null;
      for (const [i, candidate] of CANDIDATES.entries()) {
        status.textContent = `laying out ${i + 1}/${CANDIDATES.length}…`;
        // A fresh tree each time: ELK writes its answer into the object it
        // was given, so handing it the same one twice measures the second
        // run against the first one's leftovers.
        const root = buildRoot();
        root.layoutOptions = {...root.layoutOptions, ...candidate.options};
        let laid;
        try {
          laid = await elk.layout(root);
        } catch {
          // One set of options this build does not like is not a reason to
          // abandon the rest. The first candidate is the options the page
          // already runs, so there is always something to fall back to.
          continue;
        }
        const m = measureLayout(laid);
        if (!base) base = m;
        const s = scoreAgainst(base, m);
        if (!best || s < best.score) best = {laid, m, score: s, name: candidate.name};
      }
      if (!best) { flash('could not lay out again'); return; }

      positions.clear();
      // The whole picture changed, so the view is fitted to it again rather
      // than left looking at where something used to be.
      fitted = false;
      paint(best.laid);

      status.textContent = was;
      flash(`${best.name}: 折れ ${base.bends}→${best.m.bends}  ` +
        `交差 ${base.crossings}→${best.m.crossings}  縦 ${base.across}→${best.m.across}`);
    } finally {
      tidying = false;
      button.disabled = false;
      if (status.textContent.startsWith('laying out')) status.textContent = was;
    }
  }

  document.getElementById('tidy').addEventListener('click', tidy);

  // A link and a reload both land where the reader was, and the browser's own
  // back button walks the pages it walked. Nothing here invents a history: an
  // atlas page is addressable, so the address bar is the right place to keep
  // which one is open.
  if (atlas) {
    const wanted = new URLSearchParams(location.search).get('at');
    if (wanted && pages.has(wanted)) { page = pages.get(wanted); graph = page.graph; bindGraph(); }
    history.replaceState({diagram: page && page.id}, '', location.href);

    window.addEventListener('popstate', (event) => {
      const id = (event.state && event.state.diagram) || atlas.root;
      if (pages.has(id)) openDiagram(id, false);
    });

    // Backspace goes up a level. It is the gesture a file browser has, and it
    // is free here: nothing on the reading side of this page consumes it, and
    // a field that does gets it first.
    document.addEventListener('keydown', (event) => {
      if (event.key !== 'Backspace' || event.metaKey || event.ctrlKey || event.altKey) return;
      const on = event.target;
      if (on && (on.tagName === 'INPUT' || on.tagName === 'TEXTAREA' || on.isContentEditable)) return;
      const walked = trail();
      if (walked.length < 2) return;
      event.preventDefault();
      openDiagram(walked[walked.length - 2].id);
    });
  }

  buildFilters();
  buildLabelFilters();
  buildTimeline();
  updateBreadcrumbs();
  render();
})();
