// The 500-Pod input of packet FA2-T8 6.4 (N-2 information), derived from ns-perf-big.json of
// the L file: every non-Pod node; the first 500 Pods ordered by (parent or "", name) compared
// by code point; the bridges whose from.uid and to.uid are kept (a null to.uid is kept).
// Serialization: object keys sorted by code point at every level, no spaces, strings as JSON
// with non-ASCII characters written as themselves.

const byCodePoint = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

export function derive500(answer) {
  const pods = answer.nodes.filter((n) => n.kind === 'Pod');
  const kept = new Set(
    pods
      .slice()
      .sort((a, b) => byCodePoint(a.parent ?? '', b.parent ?? '') || byCodePoint(a.name, b.name))
      .slice(0, 500)
      .map((n) => n.uid),
  );
  const nodes = answer.nodes.filter((n) => n.kind !== 'Pod' || kept.has(n.uid));
  const uids = new Set(nodes.map((n) => n.uid));
  const bridges = answer.bridges.filter((b) => uids.has(b.from.uid) && (b.to.uid === null || uids.has(b.to.uid)));
  return { ...answer, nodes, bridges };
}

// canonical writes a JSON value with keys sorted by code point and no spaces.
export function canonical(value) {
  if (Array.isArray(value)) {
    return `[${value.map(canonical).join(',')}]`;
  }
  if (value && typeof value === 'object') {
    const keys = Object.keys(value).sort(byCodePoint);
    return `{${keys.map((k) => `${JSON.stringify(k)}:${canonical(value[k])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}
