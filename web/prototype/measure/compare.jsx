// The comparison scenes of packet FA2-T8 6.4: measure/compare.html?r=R1|R2&scene=S-A..S-E.
// Both candidates draw the same adapter model of the prototype's own data:
// S-A nested owner boxes (shop Deployment cart with its ReplicaSets and Pods) with the
// Service link coming from outside the box, by a two-pass layout (dagre on the boxes, then
// dagre inside the box), the layout time recorded; S-B per-link setting values and cause
// sentences (the A-3 and A-5 links); S-C the three stages; S-D the keyboard order through S-A;
// S-E the accessibility tree of S-A. window.__scene says when it is drawn and what it holds.
import 'virtual:design-system-tokens.css';
import '../app.css';
import './measure.css';
import { useCallback, useRef } from 'react';
import { createRoot } from 'react-dom/client';
import { FlowMap, MapContext, toFlow } from '../map.jsx';
import { Namespace } from '../model.js';
import { NAMESPACES } from '../data.js';
import { S } from '../strings.js';
import { clusterModel, namespaceModel } from './adapter.js';
import { drawR2 } from './r2.js';

const params = new URLSearchParams(window.location.search);
const renderer = params.get('r') === 'R2' ? 'R2' : 'R1';
const scene = ['S-A', 'S-B', 'S-C', 'S-D', 'S-E'].includes(params.get('scene')) ? params.get('scene') : 'S-A';
const result = { status: 'running', renderer, scene };
window.__scene = result;

// subset keeps the named top-level nodes of a namespace, what they own, and the links among
// them (and to missing targets).
function subset(ns, names) {
  const keep = new Set();
  for (const n of ns.answer.nodes) {
    const top = ns.top(n.uid);
    if (names.includes(`${top.kind}/${top.name}`)) {
      keep.add(n.uid);
    }
  }
  const nodes = ns.answer.nodes.filter((n) => keep.has(n.uid));
  const bridges = ns.answer.bridges.filter((b) => keep.has(b.from.uid) && (!b.to.uid || keep.has(b.to.uid)));
  return new Namespace({ ...ns.answer, nodes, bridges, refused: [], unsupported: [] });
}

function sceneParts() {
  const shop = NAMESPACES.get('shop');
  const payments = NAMESPACES.get('payments');
  if (scene === 'S-A' || scene === 'S-D' || scene === 'S-E') {
    const ns = subset(shop, ['Service/cart', 'Deployment/cart', 'ConfigMap/cart-settings', 'HorizontalPodAutoscaler/cart']);
    const t0 = performance.now();
    const model = namespaceModel(ns, { expanded: 'u-shop-deployment-cart', outer: 'dagre' });
    const layoutMs = performance.now() - t0;
    const intoBox = model.edges.filter((e) => e.to.startsWith('pod:')).length;
    return { parts: [{ ns, model }], facts: { layoutPasses: 2, layoutMs: Math.round(layoutMs * 10) / 10, linksIntoBox: intoBox } };
  }
  if (scene === 'S-B') {
    const a3 = subset(shop, ['Service/web']);
    const a5 = subset(payments, ['Deployment/payments-api']);
    return {
      parts: [
        { ns: a3, model: namespaceModel(a3) },
        { ns: a5, model: namespaceModel(a5) },
      ],
      facts: { sentences: [...a3.cards.values(), ...a5.cards.values()].flatMap((c) => c.rows).filter((r) => r.sentence).length },
    };
  }
  return {
    parts: [
      { ns: null, model: clusterModel([...NAMESPACES.values()]) },
      { ns: shop, model: namespaceModel(shop) },
      { ns: shop, model: namespaceModel(shop, { expanded: 'u-shop-deployment-cart' }) },
    ],
    facts: { stages: 3 },
  };
}

function R1Part({ ns, model, onDrawn, keyboard }) {
  const ctx = keyboard ? { ns, onCard: () => {}, onRow: () => {} } : { ns };
  return (
    <div className="compare__part">
      <MapContext.Provider value={ctx}>
        <FlowMap flow={toFlow(model)} fitKey="scene" onReady={onDrawn} />
      </MapContext.Provider>
    </div>
  );
}

function R2Part({ model, onDrawn }) {
  const ref = useRef(null);
  const started = useRef(false);
  const attach = useCallback(
    (el) => {
      ref.current = el;
      if (el && !started.current) {
        started.current = true;
        drawR2(el, model, () => requestAnimationFrame(() => requestAnimationFrame(onDrawn)), { renderEdgeLabels: scene === 'S-B' });
      }
    },
    [model, onDrawn],
  );
  return <div className="compare__part" ref={attach} />;
}

function Scene() {
  const { parts, facts } = sceneParts();
  let left = parts.length;
  const onDrawn = () => {
    left -= 1;
    if (left === 0) {
      Object.assign(result, { status: 'done', facts, parts: parts.length });
      document.getElementById('compare').dataset.status = 'done';
    }
  };
  return (
    <div className="compare">
      <h1 className="compare__title">{S.sceneTitle(renderer, scene)}</h1>
      <div className="compare__scene" id="scene">
        {parts.map((p, i) =>
          renderer === 'R1' ? <R1Part key={i} ns={p.ns} model={p.model} onDrawn={onDrawn} keyboard={scene === 'S-D' || scene === 'S-E'} /> : <R2Part key={i} model={p.model} onDrawn={onDrawn} />,
        )}
      </div>
    </div>
  );
}

createRoot(document.getElementById('compare')).render(<Scene />);
