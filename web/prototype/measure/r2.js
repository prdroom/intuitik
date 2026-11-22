// R2 (sigma + graphology, WebGL; packet FA2-T8 6.4): the adapter's model drawn as a graph.
// Each card, tile, ghost or box becomes a node at the centre of its place, sized by its width;
// each link an edge coloured by its state. Sigma draws points and lines, so owner boxes,
// card rows and row ports have no drawing here; the comparison scenes record that.
import Graph from 'graphology';
import Sigma from 'sigma';
import tokens from '../../src/design-system/tokens.js';

const NODE_COLOUR = { tile: '#52525B', card: '#3F3F46', box: '#71717A', ghost: tokens.link.broken.color, unknown: tokens.link.unknown.color, bundle: '#A1A1AA', group: '#D4D4D8', pod: '#27272A' };

function centre(n, byId) {
  let x = n.x + n.w / 2;
  let y = n.y + n.h / 2;
  if (n.parent) {
    const p = byId.get(n.parent);
    x += p.x;
    y += p.y;
  }
  return { x, y: -y };
}

export function toGraph(model) {
  const graph = new Graph({ multi: true });
  const byId = new Map(model.nodes.map((n) => [n.id, n]));
  for (const n of model.nodes) {
    const c = centre(n, byId);
    const data = n.data ?? {};
    const label = data.name ?? (data.item && data.item.card ? data.item.card.node.name : data.node ? data.node.name : n.id);
    graph.addNode(n.id, { x: c.x, y: c.y, size: Math.max(3, n.w / 24), label, color: NODE_COLOUR[n.type] ?? '#3F3F46' });
  }
  for (const e of model.edges) {
    if (graph.hasNode(e.from) && graph.hasNode(e.to)) {
      graph.addEdgeWithKey(e.id, e.from, e.to, { color: tokens.link[e.state].color, size: tokens.link[e.state].width, label: e.sentence || undefined });
    }
  }
  return graph;
}

// drawR2 renders into container and calls onDrawn after the first frame sigma rendered.
export function drawR2(container, model, onDrawn, settings = {}) {
  const graph = toGraph(model);
  const renderer = new Sigma(graph, container, { renderLabels: true, labelRenderedSizeThreshold: 4, allowInvalidContainer: true, ...settings });
  let done = false;
  renderer.on('afterRender', () => {
    if (!done) {
      done = true;
      onDrawn(renderer);
    }
  });
  renderer.refresh();
  return renderer;
}
