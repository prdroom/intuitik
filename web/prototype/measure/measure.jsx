// The renderer measurement page (packet FA2-T8 6.4): measure/index.html?r=R1|R2&view=far
// or &view=ns&input=pods500|perf-big[&cap=<n>]. It checks the L file's sha256 before anything
// else and refuses a different file. Then it parses the answers (data-parsed), builds the
// adapter's model and draws it with the chosen renderer (far-drawn = laid out and two
// animation frames run). The Playwright case reads window.__measure and calls
// window.__longtasks around its scripted zoom and pan actions.
import 'virtual:design-system-tokens.css';
import '../app.css';
import './measure.css';
import { createRoot } from 'react-dom/client';
import { FlowMap, MapContext, toFlow } from '../map.jsx';
import { Namespace } from '../model.js';
import { clusterModel, namespaceModel } from './adapter.js';
import { derive500 } from './derive500.js';
import input from './input.json';
import { drawR2 } from './r2.js';
import { gunzip, readTar, sha256Hex } from './tar.js';

const params = new URLSearchParams(window.location.search);
const renderer = params.get('r') === 'R2' ? 'R2' : 'R1';
const view = params.get('view') === 'ns' ? 'ns' : 'far';
const source = params.get('input') === 'pods500' ? 'pods500' : 'perf-big';
const cap = params.has('cap') ? Number(params.get('cap')) : Infinity;

const supported = typeof PerformanceObserver !== 'undefined' && (PerformanceObserver.supportedEntryTypes ?? []).includes('longtask');
const longtasks = [];
let observer = null;
if (supported) {
  observer = new PerformanceObserver((list) => longtasks.push(...list.getEntries()));
  observer.observe({ type: 'longtask', buffered: true });
}

const result = { status: 'running', renderer, view, source: view === 'ns' ? source : 'L', cap: Number.isFinite(cap) ? cap : null, supported };
window.__measure = result;
const root = document.getElementById('measure');

function finish(fields) {
  Object.assign(result, fields);
  root.dataset.status = result.status;
}

const frame = () => new Promise((resolve) => requestAnimationFrame(() => resolve()));

// __longtasks counts the long tasks that started in [since, until): it waits two animation
// frames and 500 ms first, then takes the observer's pending records.
window.__longtasks = async (since, until = Infinity) => {
  await frame();
  await frame();
  await new Promise((resolve) => setTimeout(resolve, 500));
  if (observer) {
    longtasks.push(...observer.takeRecords());
  }
  const list = longtasks.filter((e) => e.startTime >= since && e.startTime < until);
  return { over200: list.filter((e) => e.duration > 200).length, longest: Math.round(Math.max(0, ...list.map((e) => e.duration))) };
};

// __busy queues one page task that keeps the main thread busy for ms milliseconds (the control
// of every long-task run). It runs as its own task of the page, as an action's work would; a
// script evaluated by the test driver is not reported by the long-task observer.
window.__busy = (ms) => {
  setTimeout(() => {
    const end = performance.now() + ms;
    while (performance.now() < end) {
      // busy wait
    }
  }, 0);
};

window.__now = () => performance.now();

function draw(model, ns, parsed) {
  const drawn = () => {
    const at = performance.now();
    finish({ status: 'done', parsed, drawn: at, pageToFar: at, renderFar: at - parsed, nodes: model.nodes.length, edges: model.edges.length });
  };
  if (renderer === 'R1') {
    createRoot(root).render(
      <MapContext.Provider value={{ ns }}>
        <div className="measure-canvas">
          <FlowMap flow={toFlow(model)} fitKey="measure" onReady={drawn} />
        </div>
      </MapContext.Provider>,
    );
    return;
  }
  const canvas = document.createElement('div');
  canvas.className = 'measure-canvas';
  root.appendChild(canvas);
  drawR2(canvas, model, () => frame().then(frame).then(drawn));
}

async function main() {
  if (!supported) {
    finish({ status: 'error', error: 'ERROR longtask observer not supported' });
    return;
  }
  const bytes = new Uint8Array(await (await fetch(input.file)).arrayBuffer());
  const sha = await sha256Hex(bytes);
  result.sha256 = sha;
  result.bytes = bytes.length;
  if (sha !== input.sha256) {
    finish({ status: 'refused', exit: 1, line: `input sha256 mismatch expected=${input.sha256} got=${sha}` });
    return;
  }
  const files = readTar(await gunzip(bytes));
  const text = new TextDecoder();
  const parse = (name) => JSON.parse(text.decode(files.get(name)));
  if (view === 'far') {
    const answers = parse('cluster.json').namespaces.items.map((n) => parse(`ns-${n}.json`));
    const parsed = performance.now();
    draw(clusterModel(answers.map((a) => new Namespace(a)), { columns: 8 }), null, parsed);
    return;
  }
  let answer = parse('ns-perf-big.json');
  if (source === 'pods500') {
    answer = derive500(answer);
  }
  const parsed = performance.now();
  const ns = new Namespace(answer);
  draw(namespaceModel(ns, { cap }), ns, parsed);
}

main().catch((err) => finish({ status: 'error', error: `ERROR ${err && err.message}` }));
