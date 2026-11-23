// The one adapter from a graph answer to a renderer model (packet FA2-T8 6.4): positioned
// nodes (cards, tiles, ghosts, bundles, owner boxes) and links between row ports. The screens
// (R1) and both measured candidates (R1, R2) draw this model, so the candidates differ only in
// how they draw it. Layout: the Cluster stage is a grid of tiles; the Namespace stage is the
// columns of UI.md 3; an expanded workload is laid out inside its box with dagre (pass two)
// after the boxes around it are placed (pass one: the columns, or dagre for scene S-A).
import dagre from '@dagrejs/dagre';
import { foldRows } from '../../src/design-system/components/map.jsx';
import { rowCenters, inPortCenter } from '../../src/design-system/geometry.js';
import { columnOf, compareTiles } from '../model.js';

export const CARD_W = 272;
export const COL_GAP = 120;
export const ROW_GAP = 28;
const HEADER = 44;
const ROW = 30;
const ROW_TWO = 48;
const ROW_SEP = 4;
const PAD = 6;
const FOOTER = 34;
const TILE_W = 272;
const TILE_H = 132;
const GHOST_H = 96;
const BUNDLE_H = 72;
const POD_W = 220;
const POD_H = 56;
const BOX_PAD = 16;
const BOX_HEAD = 40;

const rowHeight = (row) => (row.state !== 'connected' && row.sentence ? ROW_TWO : ROW);

// cardGeometry is the height of a compact card and the y of each visible row's port, from the
// design system's own fold rule and port geometry.
export function cardGeometry(rows, chips, mode = 'compact') {
  const visible = foldRows(rows.map((r) => ({ ...r, id: r.id })), mode);
  const heights = visible.map((v) => (v.type === 'fold' ? ROW : rowHeight(v.row)));
  const centres = rowCenters(heights);
  const ports = {};
  visible.forEach((v, i) => {
    if (v.type === 'fold') {
      for (const r of rows.filter((x) => x.state === 'connected').slice(2)) {
        ports[r.id] = centres[i];
      }
    } else {
      ports[v.row.id] = centres[i];
    }
  });
  const rowsH = heights.reduce((a, h) => a + h + ROW_SEP, 0);
  const height = HEADER + (heights.length ? rowsH + 2 * PAD : 0) + (chips > 0 ? FOOTER : 0);
  return { height, ports, inPort: inPortCenter() };
}

// clusterModel: one tile per namespace, sorted by problem score then name (UI.md 3).
export function clusterModel(namespaces, { columns = 4 } = {}) {
  const tiles = namespaces.map((ns) => ({ name: ns.name, tile: ns.tile(), ns })).sort(compareTiles);
  const nodes = tiles.map((t, i) => ({
    id: `tile:${t.name}`,
    type: 'tile',
    x: (i % columns) * (TILE_W + 32),
    y: Math.floor(i / columns) * (TILE_H + 32),
    w: TILE_W,
    h: TILE_H,
    data: { name: t.name, tile: t.tile },
  }));
  return { nodes, edges: [] };
}

function anomalous(card, ns) {
  return card.rows.some((r) => r.state !== 'connected') || ns.status(card.node.uid) !== null;
}

// namespaceModel lays out one namespace. `expanded` is the uid of a workload shown with its
// owned objects (Workload stage); `cap` bundles a column's cards over that count (UI.md 3 many
// resources); `outer` is 'columns' (UI.md 3) or 'dagre' (scene S-A, pass one by dagre).
export function namespaceModel(ns, { expanded = null, cap = Infinity, outer = 'columns' } = {}) {
  const items = [];
  const placeholders = new Map();
  for (const card of ns.cards.values()) {
    const chips = card.node.kind === 'Pod' || card.node.owner.text ? 1 : ns.pods(card.node.uid).length > 0 ? 1 : 0;
    const geo = cardGeometry(card.rows, chips);
    items.push({ id: card.node.uid, type: 'card', kind: card.node.kind, card, geo, anomalous: anomalous(card, ns) });
    for (const row of card.rows) {
      if (row.target && row.target.type !== 'node' && !placeholders.has(row.target.id)) {
        placeholders.set(row.target.id, row.target);
      }
    }
  }
  for (const t of placeholders.values()) {
    items.push({ id: t.id, type: t.type, kind: t.kind, target: t, geo: { height: t.type === 'ghost' ? GHOST_H : HEADER + FOOTER, inPort: inPortCenter(), ports: {} }, anomalous: true });
  }

  const nodes = [];
  const boxOf = new Map();
  if (expanded) {
    const box = workloadBox(ns, expanded);
    const item = items.find((it) => it.id === expanded);
    item.type = 'box';
    item.box = box;
    item.geo = { ...item.geo, height: item.geo.height + box.h + BOX_PAD };
    boxOf.set(expanded, box);
  }

  // Pass one: place the top-level items.
  const placed = new Map();
  if (outer === 'dagre') {
    const g = new dagre.graphlib.Graph();
    g.setGraph({ rankdir: 'LR', nodesep: ROW_GAP, ranksep: COL_GAP });
    g.setDefaultEdgeLabel(() => ({}));
    for (const it of items) {
      g.setNode(it.id, { width: it.type === 'box' ? Math.max(CARD_W, it.box.w) : CARD_W, height: it.geo.height });
    }
    for (const it of items.filter((x) => x.card)) {
      for (const row of it.card.rows) {
        if (row.target) {
          g.setEdge(it.id, row.target.id);
        }
      }
    }
    dagre.layout(g);
    for (const it of items) {
      const p = g.node(it.id);
      placed.set(it.id, { x: p.x - p.width / 2, y: p.y - p.height / 2, w: p.width });
    }
  } else {
    const columns = new Map();
    for (const it of items) {
      const c = columnOf(it.kind);
      if (!columns.has(c)) {
        columns.set(c, []);
      }
      columns.get(c).push(it);
    }
    let x = 0;
    for (const c of [...columns.keys()].sort((a, b) => a - b)) {
      const list = columns.get(c).sort((a, b) => Number(b.anomalous) - Number(a.anomalous) || (a.type === 'card' ? 0 : 1) - (b.type === 'card' ? 0 : 1) || nameOf(a).localeCompare(nameOf(b)));
      const shown = list.slice(0, cap);
      const rest = list.slice(cap);
      const width = Math.max(CARD_W, ...shown.map((it) => (it.type === 'box' ? it.box.w : CARD_W)));
      let y = 0;
      for (const it of shown) {
        placed.set(it.id, { x, y, w: it.type === 'box' ? it.box.w : CARD_W });
        y += it.geo.height + ROW_GAP;
      }
      const byKind = new Map();
      for (const it of rest) {
        if (!byKind.has(it.kind)) {
          byKind.set(it.kind, []);
        }
        byKind.get(it.kind).push(it);
      }
      for (const [kind, list2] of byKind) {
        const id = `bundle:${c}:${kind}`;
        const notConnected = list2.filter((it) => it.anomalous).length;
        nodes.push({ id, type: 'bundle', x, y, w: CARD_W, h: BUNDLE_H, data: { kind, count: list2.length, notConnected } });
        for (const it of list2) {
          placed.set(it.id, { bundle: id, x, y: y + BUNDLE_H / 2 - inPortCenter(), w: CARD_W });
        }
        y += BUNDLE_H + ROW_GAP;
      }
      x += width + COL_GAP;
    }
  }

  for (const it of items) {
    const p = placed.get(it.id);
    if (p.bundle) {
      continue;
    }
    nodes.push({ id: it.id, type: it.type, x: p.x, y: p.y, w: p.w, h: it.geo.height, data: { item: it } });
    if (it.type === 'box') {
      const box = boxOf.get(it.id);
      const top = it.geo.height - box.h;
      for (const inner of box.nodes) {
        nodes.push({ ...inner, parent: it.id, x: inner.x + BOX_PAD, y: inner.y + top - BOX_PAD / 2 });
      }
    }
  }

  // Links: one per row, from the row's port to the target's in port. A service row into an
  // expanded workload splits into one link per Pod (UI.md 3 Workload stage).
  const edges = [];
  const endOf = (id) => {
    const p = placed.get(id);
    return p && p.bundle ? p.bundle : id;
  };
  for (const it of items.filter((x) => x.card)) {
    const from = endOf(it.id);
    for (const row of it.card.rows) {
      if (!row.target) {
        continue;
      }
      const base = { state: row.state, kind: row.kind, rowId: row.id, sourceHandle: from === it.id ? row.id : undefined, sentence: row.sentence, fromName: `${it.card.node.kind} ${it.card.node.name}` };
      if (row.target.type === 'node' && boxOf.has(row.target.id) && row.kind === 'service-pod') {
        for (const b of row.bridges) {
          edges.push({ ...base, id: `${row.id}:${b.to.uid}`, from, to: `pod:${b.to.uid}`, toName: `Pod ${b.to.name}` });
        }
        continue;
      }
      edges.push({ ...base, id: row.id, from, to: endOf(row.target.id), toName: targetName(ns, row.target) });
    }
  }
  return { nodes, edges };
}

function nameOf(it) {
  return it.card ? it.card.node.name : it.id;
}

function targetName(ns, target) {
  if (target.type === 'node') {
    const n = ns.byUid.get(target.id);
    return `${n.kind} ${n.name}`;
  }
  return target.selector ? `Pods ${target.selector}` : `${target.kind} ${target.name}`;
}

// workloadBox lays out the owned objects of a workload (pass two): ReplicaSet boxes holding
// their Pods, by dagre on a compound graph. Positions are relative to the box's inner area.
export function workloadBox(ns, uid) {
  const g = new dagre.graphlib.Graph({ compound: true });
  g.setGraph({ rankdir: 'TB', nodesep: 16, ranksep: 24, marginx: 0, marginy: 0 });
  g.setDefaultEdgeLabel(() => ({}));
  const children = ns.children(uid);
  for (const c of children) {
    if (c.kind === 'Pod') {
      g.setNode(`pod:${c.uid}`, { width: POD_W, height: POD_H, node: c });
      continue;
    }
    g.setNode(`group:${c.uid}`, { node: c, paddingTop: BOX_HEAD });
    const pods = ns.children(c.uid).filter((p) => p.kind === 'Pod');
    for (const p of pods) {
      g.setNode(`pod:${p.uid}`, { width: POD_W, height: POD_H, node: p });
      g.setParent(`pod:${p.uid}`, `group:${c.uid}`);
    }
    if (pods.length === 0) {
      g.setNode(`empty:${c.uid}`, { width: POD_W, height: 24 });
      g.setParent(`empty:${c.uid}`, `group:${c.uid}`);
    }
  }
  dagre.layout(g);
  const nodes = [];
  let w = 0;
  let h = 0;
  for (const id of g.nodes()) {
    const n = g.node(id);
    if (id.startsWith('empty:')) {
      continue;
    }
    const x = n.x - n.width / 2;
    const y = n.y - n.height / 2;
    w = Math.max(w, x + n.width);
    h = Math.max(h, y + n.height);
    nodes.push({ id, type: id.startsWith('pod:') ? 'pod' : 'group', x, y, w: n.width, h: n.height, data: { node: n.node }, parentGroup: g.parent(id) });
  }
  // Parents first, so that a renderer meets a box before what it holds.
  nodes.sort((a, b) => (a.type === 'group' ? 0 : 1) - (b.type === 'group' ? 0 : 1));
  return { nodes, w: Math.max(CARD_W, w + 2 * BOX_PAD), h: h + BOX_PAD };
}
