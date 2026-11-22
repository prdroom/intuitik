// The prototype's view of the fixed data (packet FA2-T8 6.2): the graph answers in the shape
// of the merged server, read as the map, the Needs attention panel and Find read them.
// Nothing here fetches; object files (Details, YAML) are fetched on demand by the Inspector.
import { bridgeSentence } from './strings.js';

export const WORKLOAD_KINDS = ['Deployment', 'StatefulSet', 'DaemonSet', 'CronJob', 'Job'];

// Columns of the Namespace stage, left to right = request flow (UI.md 3).
const COLUMN_OF = {
  Ingress: 0,
  HTTPRoute: 0,
  Service: 1,
  HorizontalPodAutoscaler: 1,
  Deployment: 2,
  StatefulSet: 2,
  DaemonSet: 2,
  CronJob: 2,
  Job: 2,
  ReplicaSet: 2,
  Pod: 2,
  ConfigMap: 3,
  Secret: 3,
  PersistentVolumeClaim: 3,
  PersistentVolume: 4,
  StorageClass: 4,
};

export const columnOf = (kind) => COLUMN_OF[kind] ?? 2;

const SEVERITY = ['broken', 'warning', 'unknown', 'waiting', 'optional', 'connected'];
const worst = (states) => SEVERITY.find((s) => states.includes(s)) ?? 'connected';

const SHORT_KIND = { HorizontalPodAutoscaler: 'HPA', PersistentVolumeClaim: 'PVC', PersistentVolume: 'PV' };
export const shortKind = (kind) => SHORT_KIND[kind] ?? kind;

const ROW_NAME = { selector: 'selects' };

// rowParts splits the settings of a bridge into the short name and the value of its card row
// (UI.md 4.5): "selector app=web", ":80->8080" -> "selects" / "app=web \u00b7 :80->8080".
export function rowParts(settings) {
  const unique = [...new Set(settings)];
  const first = unique[0] ?? '';
  const i = first.indexOf(' ');
  const head = i < 0 ? first : first.slice(0, i);
  const rest = i < 0 ? [] : [first.slice(i + 1).trim()];
  return { name: ROW_NAME[head] ?? head, value: rest.concat(unique.slice(1)).join(' \u00b7 ') };
}

// Namespace is one graph answer with its derived parts.
export class Namespace {
  constructor(answer) {
    this.answer = answer;
    this.name = answer.namespace;
    this.byUid = new Map(answer.nodes.map((n) => [n.uid, n]));
    this.bridges = answer.bridges.map((b, i) => ({ ...b, id: `${this.name}:b${i}` }));
    this.cards = this.buildCards();
  }

  get empty() {
    return this.answer.namespaceState.state === 'empty' || this.answer.nodes.length === 0;
  }

  // top is the node a node sits in at the Namespace stage: its outermost owner.
  top(uid) {
    let n = this.byUid.get(uid);
    while (n && n.parent && this.byUid.has(n.parent)) {
      n = this.byUid.get(n.parent);
    }
    return n;
  }

  children(uid) {
    return this.answer.nodes.filter((n) => n.parent === uid);
  }

  pods(uid) {
    const out = [];
    const walk = (u) => {
      for (const c of this.children(u)) {
        if (c.kind === 'Pod') {
          out.push(c);
        }
        walk(c.uid);
      }
    };
    walk(uid);
    return out;
  }

  // The target of a bridge on the map: the card of the node it reaches, a ghost when the target
  // is missing (for a claim without a volume: the volume it waits for or cannot get), or an
  // unknown placeholder when it cannot be read.
  targetOf(b) {
    if (b.to.uid && this.byUid.has(b.to.uid)) {
      return { type: 'node', id: this.top(b.to.uid).uid };
    }
    if (b.state === 'unknown') {
      return { type: 'unknown', id: `unknown:${b.to.kind}:${b.to.name}`, kind: b.to.kind, name: b.to.name };
    }
    if (b.kind === 'pvc-pv') {
      const claim = this.byUid.get(b.from.uid).name;
      return { type: 'ghost', id: `ghost:PersistentVolume:${claim}`, kind: 'PersistentVolume', claim, variant: b.state === 'waiting' ? 'waiting' : 'broken' };
    }
    if (b.kind === 'service-pod') {
      const selector = (b.settings.find((s) => s.startsWith('selector ')) ?? '').slice('selector '.length);
      return { type: 'ghost', id: `ghost:Pod:${selector}`, kind: 'Pod', selector, variant: 'broken' };
    }
    return { type: 'ghost', id: `ghost:${b.to.kind}:${b.to.name}`, kind: b.to.kind, name: b.to.name, variant: b.state === 'optional' ? 'optional' : 'broken' };
  }

  // Cards of the Namespace stage: one per top-level node, with one row per target (UI.md 4.5:
  // the rows of one target are merged), each row naming the bridges it stands for.
  buildCards() {
    const cards = new Map();
    for (const n of this.answer.nodes) {
      if (!n.parent || !this.byUid.has(n.parent)) {
        cards.set(n.uid, { node: n, rows: [] });
      }
    }
    const rows = new Map();
    for (const b of this.bridges) {
      const from = this.top(b.from.uid);
      const target = this.targetOf(b);
      const key = `${from.uid}|${b.kind}|${target ? target.id : `none:${b.to.kind}`}|${b.state}`;
      let row = rows.get(key);
      if (!row) {
        row = { id: `${this.name}:r${rows.size}`, from: from.uid, kind: b.kind, target, bridges: [], settings: [] };
        rows.set(key, row);
        cards.get(from.uid).rows.push(row);
      }
      row.bridges.push(b);
      row.settings.push(...b.settings);
    }
    for (const row of rows.values()) {
      row.state = worst(row.bridges.map((b) => b.state));
      const first = row.bridges.find((b) => b.state === row.state);
      row.text = first.text ?? '';
      row.sentence = row.state === 'connected' ? '' : bridgeSentence(row.state, row.text);
      Object.assign(row, rowParts(row.settings));
    }
    for (const card of cards.values()) {
      card.rows.sort((a, b) => SEVERITY.indexOf(a.state) - SEVERITY.indexOf(b.state));
    }
    return cards;
  }

  rowById(id) {
    for (const card of this.cards.values()) {
      const row = card.rows.find((r) => r.id === id);
      if (row) {
        return row;
      }
    }
    return null;
  }

  // The node-state tab of a card (UI.md 4.3): a failing, unknown or progressing Pod inside it,
  // else the card's own Pod state, else broken links as the attention mark.
  status(uid) {
    const card = this.cards.get(uid);
    const node = card.node;
    const pods = node.kind === 'Pod' ? [node] : this.pods(uid);
    const broken = card.rows.filter((r) => r.state === 'broken').length;
    for (const state of ['failing', 'unknown', 'progressing']) {
      const pod = pods.find((p) => p.nodeState && p.nodeState.state === state);
      if (pod) {
        return { state, text: pod.nodeState.text, brokenLinks: broken };
      }
    }
    return broken > 0 ? { state: 'attention', brokenLinks: broken } : null;
  }

  podCounts(uid) {
    const pods = uid ? (this.byUid.get(uid).kind === 'Pod' ? [this.byUid.get(uid)] : this.pods(uid)) : this.answer.nodes.filter((n) => n.kind === 'Pod');
    const c = { ok: 0, progressing: 0, failing: 0, unknown: 0, total: pods.length };
    for (const p of pods) {
      const s = p.nodeState ? p.nodeState.state : 'healthy';
      c[s === 'healthy' ? 'ok' : s]++;
    }
    return c;
  }

  // Tile numbers (UI.md 3, C-16): each number counts items the Namespace stage shows.
  tile() {
    const rows = [...this.cards.values()].flatMap((c) => c.rows);
    const count = (s) => rows.filter((r) => r.state === s).length;
    const pods = this.podCounts(null);
    const workloads = [...this.cards.values()].filter((c) => WORKLOAD_KINDS.includes(c.node.kind) || c.node.kind === 'Pod' || c.node.kind === 'ReplicaSet').length;
    return {
      workloads,
      pods: { ok: pods.ok, progressing: pods.progressing, failing: pods.failing },
      anomalies: { broken: count('broken'), failing: pods.failing, warning: count('warning'), waiting: count('waiting'), unknown: count('unknown') + pods.unknown, optional: count('optional') },
    };
  }

  // Needs attention items (UI.md 5), grouped Broken, Failing, Warning, Unknown, Waiting.
  attention() {
    const groups = { broken: [], failing: [], warning: [], unknown: [], waiting: [] };
    for (const card of this.cards.values()) {
      for (const row of card.rows) {
        if (groups[row.state]) {
          groups[row.state].push({ id: row.id, ns: this.name, location: `${this.name} \u203a ${shortKind(card.node.kind)} ${card.node.name}`, value: `${row.name} ${row.value}`, reason: row.sentence });
        }
      }
    }
    for (const pod of this.answer.nodes.filter((n) => n.kind === 'Pod' && n.nodeState)) {
      const s = pod.nodeState.state;
      if (s === 'failing' || s === 'unknown') {
        groups[s].push({ id: pod.uid, ns: this.name, location: `${this.name} \u203a Pod ${pod.name}`, value: `${this.top(pod.uid).kind} ${this.top(pod.uid).name}`, reason: pod.nodeState.text });
      }
    }
    return groups;
  }
}

// score orders the Cluster stage tiles (UI.md 3): Broken and Failing, then Warning, Unknown
// and Waiting; equal scores by name.
export function tileScore(t) {
  const a = t.anomalies;
  return [a.broken + a.failing, a.warning, a.unknown, a.waiting];
}

export function compareTiles(a, b) {
  const sa = tileScore(a.tile);
  const sb = tileScore(b.tile);
  for (let i = 0; i < sa.length; i++) {
    if (sa[i] !== sb[i]) {
      return sb[i] - sa[i];
    }
  }
  return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
}

// find matches kind, name and namespace case-insensitively and orders exact name matches,
// then name prefixes, then other matches; ties by namespace, then name (packet 3.5). Only
// resources the data says were read are found.
export function find(namespaces, query) {
  const q = query.trim().toLowerCase();
  if (!q) {
    return [];
  }
  const hits = [];
  for (const ns of namespaces) {
    for (const n of ns.answer.nodes) {
      const name = n.name.toLowerCase();
      if (name.includes(q) || n.kind.toLowerCase().includes(q) || ns.name.toLowerCase().includes(q)) {
        const rank = name === q ? 0 : name.startsWith(q) ? 1 : 2;
        hits.push({ rank, ns: ns.name, uid: n.uid, kind: n.kind, name: n.name });
      }
    }
  }
  const cmp = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
  return hits.sort((a, b) => a.rank - b.rank || cmp(a.ns, b.ns) || cmp(a.name, b.name));
}
