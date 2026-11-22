// R1 (@xyflow/react, packet FA2-T8 6.4): the map drawn from the adapter's model with the
// design system's cards, tiles, ghosts and links. The screens and the R1 measurement use this
// one component; the measurement passes no handlers.
import { createContext, useContext, useEffect, useMemo, useRef } from 'react';
import { Handle, Position, ReactFlow, ReactFlowProvider, getBezierPath, useReactFlow } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { Chip, GhostCard, NamespaceTile, NodeCard, StatusTab, foldRows } from '../src/design-system/index.js';
import { brokenLinks, linkAccessibleName } from '../src/design-system/strings.js';
import tokens from '../src/design-system/tokens.js';
import { shortKind } from './model.js';
import { S, stateWord } from './strings.js';

export const MapContext = createContext({});

const LINK_KIND = {
  'ingress-service': 'Traffic',
  'httproute-service': 'Traffic',
  'service-pod': 'Traffic',
  'pod-pvc': 'Storage',
  'pvc-pv': 'Storage',
  'pvc-storageclass': 'Storage',
  'pv-storageclass': 'Storage',
  'pod-configmap': 'Config',
  'pod-secret': 'Config',
  'hpa-target': 'Scaling',
};
export const linkKind = (kind) => LINK_KIND[kind] ?? 'Config';

// moveFocus implements the arrow keys of UI.md 9: between the map's nodes in column order.
function moveFocus(el, step) {
  const all = Array.from(document.querySelectorAll('.proto-node'));
  const i = all.indexOf(el);
  const next = all[i + step];
  if (next) {
    next.focus();
  }
}

function NodeShell({ control, target, label, onSelect, onExpand, children, className = '' }) {
  const onKeyDown = (e) => {
    if (e.target !== e.currentTarget) {
      return;
    }
    if (e.key === 'Enter' && e.shiftKey && onExpand) {
      e.preventDefault();
      onExpand();
    } else if (e.key === 'Enter' && onSelect) {
      e.preventDefault();
      onSelect();
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
      e.preventDefault();
      moveFocus(e.currentTarget, 1);
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
      e.preventDefault();
      moveFocus(e.currentTarget, -1);
    }
  };
  if (!onSelect) {
    return (
      <div className={`proto-node-static ${className}`} data-target={target}>
        {children}
      </div>
    );
  }
  return (
    <div
      className={`proto-node ${className}`}
      role="button"
      tabIndex={0}
      data-control={control}
      data-target={target}
      aria-label={label}
      onClick={onSelect}
      onDoubleClick={onExpand}
      onKeyDown={onKeyDown}
    >
      {children}
    </div>
  );
}

function Ports({ geo }) {
  return (
    <>
      <Handle type="target" position={Position.Left} id="in" className="proto-handle" style={{ top: geo.inPort }} isConnectable={false} />
      {Object.entries(geo.ports).map(([id, y]) => (
        <Handle key={id} type="source" position={Position.Right} id={id} className="proto-handle" style={{ top: y }} isConnectable={false} />
      ))}
    </>
  );
}

function chipsOf(ns, node) {
  if (node.owner && node.owner.text) {
    return [{ text: node.owner.text, tone: 'unknown' }];
  }
  const unsupported = (ns.answer.unsupported ?? []).find((u) => u.kind === node.kind && node.apiVersion.endsWith(`/${u.version}`));
  if (unsupported) {
    return [{ text: unsupported.text, tone: 'unknown' }];
  }
  const c = ns.podCounts(node.uid);
  if (node.kind === 'Pod') {
    return [];
  }
  if (c.total > 0) {
    return [{ text: S.podsReady(c.ok, c.total), tone: c.ok === c.total ? 'neutral' : 'attention' }];
  }
  return [];
}

function statusOf(ns, uid) {
  const st = ns.status(uid);
  if (!st) {
    return undefined;
  }
  if (st.state === 'attention') {
    return { state: 'attention', text: brokenLinks(st.brokenLinks) };
  }
  return st;
}

function CardNode({ data }) {
  const ctx = useContext(MapContext);
  const { item } = data;
  const ns = ctx.ns;
  const node = item.card.node;
  const rows = item.card.rows.map((r) => ({ id: r.id, state: r.state, name: r.name, value: r.value, reason: r.sentence || undefined }));
  const selected = ctx.sel && ctx.sel.t === 'node' && ctx.sel.id === node.uid;
  const expandable = item.type === 'card' && ns.pods(node.uid).length > 0 && node.kind !== 'Pod';
  const target = `${ns.name}/${node.kind}/${node.name}`;
  const mode = item.type === 'box' ? 'expanded' : 'compact';
  const shown = new Set(foldRows(rows, mode).filter((v) => v.type === 'row').map((v) => v.row.id));
  return (
    <NodeShell
      control="card"
      target={target}
      className={item.type === 'box' ? 'proto-node--box' : ''}
      label={`${node.kind} ${node.name}`}
      onSelect={ctx.onCard && (() => ctx.onCard({ t: 'node', id: node.uid }))}
      onExpand={ctx.onExpand && expandable ? () => ctx.onExpand(node.uid) : undefined}
    >
      <NodeCard kind={shortKind(node.kind)} name={node.name} rows={rows} chips={chipsOf(ns, node)} status={statusOf(ns, node.uid)} mode={mode} selected={selected} />
      {ctx.onRow &&
        item.card.rows
          .filter((r) => shown.has(r.id))
          .map((r) => (
            <button
              key={r.id}
              type="button"
              className={`proto-row-hit${ctx.sel && ctx.sel.t === 'row' && ctx.sel.id === r.id ? ' proto-row-hit--selected' : ''}`}
              data-control="card"
              data-target={`row:${r.id}`}
              aria-label={`${r.name} ${r.value}${r.sentence ? `, ${r.sentence}` : ''}`}
              style={{ top: item.geo.ports[r.id] - (r.state !== 'connected' && r.sentence ? 24 : 15), height: r.state !== 'connected' && r.sentence ? 48 : 30 }}
              onClick={(e) => {
                e.stopPropagation();
                ctx.onRow(r.id);
              }}
            />
          ))}
      {ctx.onExpand && expandable && (
        <button type="button" className="proto-expand" data-control="expand" data-target={target} aria-label={`${S.stage.workload}: ${node.kind} ${node.name}`} onClick={(e) => { e.stopPropagation(); ctx.onExpand(node.uid); }}>
          {'\u2922'}
        </button>
      )}
      <Ports geo={item.geo} />
    </NodeShell>
  );
}

function GhostNode({ data }) {
  const ctx = useContext(MapContext);
  const t = data.item.target;
  const looking = S.ghostTitle(t);
  const result = t.selector ? S.ghostIn(0, ctx.ns.name) : '';
  const description = t.selector ? S.ghostLooking(t.selector) : undefined;
  return (
    <NodeShell control="card" target={`${ctx.ns.name}/${t.id}`} label={looking} onSelect={ctx.onCard && (() => ctx.onCard({ t: 'ghost', id: t.id }))}>
      <div className={ctx.sel && ctx.sel.id === t.id ? 'proto-selected' : ''}>
        <GhostCard variant={t.variant} kind={shortKind(t.kind)} target={looking} result={result} description={description} />
      </div>
      <Ports geo={data.item.geo} />
    </NodeShell>
  );
}

function UnknownNode({ data }) {
  const ctx = useContext(MapContext);
  const t = data.item.target;
  return (
    <NodeShell control="card" target={`${ctx.ns.name}/${t.id}`} label={`${t.kind} ${t.name}`} onSelect={ctx.onCard && (() => ctx.onCard({ t: 'unknown', id: t.id }))}>
      <NodeCard kind={t.kind} name={t.name} rows={[]} chips={[]} status={{ state: 'unknown', text: S.noPermission }} selected={ctx.sel && ctx.sel.id === t.id} />
      <Ports geo={data.item.geo} />
    </NodeShell>
  );
}

function TileNode({ data }) {
  const ctx = useContext(MapContext);
  const { name, tile, noPermission } = data;
  return (
    <NodeShell control="tile" target={name} label={name} onSelect={ctx.onTile && !noPermission ? () => ctx.onTile(name) : undefined}>
      <NamespaceTile name={name} workloads={tile.workloads} pods={tile.pods} anomalies={tile.anomalies} labels={S.tileLabels} noPermission={noPermission ? S.noPermission : undefined} />
    </NodeShell>
  );
}

function BundleNode({ data }) {
  return (
    <div className="proto-bundle">
      <span className="proto-bundle__text">{S.more(data.count, data.kind)}</span>
      {data.notConnected > 0 && <Chip tone="attention">{S.summaryCount(data.notConnected, stateWord('warning').toLowerCase())}</Chip>}
      <Handle type="target" position={Position.Left} id="in" className="proto-handle" style={{ top: 22 }} isConnectable={false} />
      <Handle type="source" position={Position.Right} id="out" className="proto-handle" style={{ top: 22 }} isConnectable={false} />
    </div>
  );
}

function GroupNode({ data }) {
  const node = data.node;
  return (
    <div className="proto-group">
      <span className="proto-group__kind">{node.kind}</span> <span className="proto-group__name">{node.name}</span>
    </div>
  );
}

function PodNode({ data, id }) {
  const ctx = useContext(MapContext);
  const pod = data.node;
  const replaced = ctx.replaced && ctx.replaced[pod.name];
  const name = replaced ?? pod.name;
  const st = pod.nodeState && pod.nodeState.state !== 'healthy' ? pod.nodeState : null;
  return (
    <NodeShell control="card" target={`${ctx.ns.name}/Pod/${pod.name}`} label={`Pod ${name}`} onSelect={ctx.onCard && (() => ctx.onCard({ t: 'node', id: pod.uid }))}>
      <div className={`proto-pod${ctx.sel && ctx.sel.id === pod.uid ? ' proto-pod--selected' : ''}`} data-pod={id}>
        {st && <StatusTab state={st.state} text={st.text} />}
        <span className="proto-pod__kind">Pod</span> <span className="proto-pod__name">{name}</span>
        {replaced && <span className="proto-pod__note">{S.replaced(pod.name, replaced)}</span>}
      </div>
      <Handle type="target" position={Position.Left} id="in" className="proto-handle" style={{ top: 28 }} isConnectable={false} />
    </NodeShell>
  );
}

function DsEdge({ sourceX, sourceY, targetX, targetY, data }) {
  const [path] = getBezierPath({ sourceX, sourceY, sourcePosition: Position.Right, targetX, targetY, targetPosition: Position.Left });
  const style = tokens.link[data.state];
  const m = 4;
  return (
    <g className={`ds-link ds-link--${data.state}${data.selected ? ' proto-link--selected' : ''}`}>
      <path className="ds-link__line" d={path} strokeWidth={data.selected ? style.width + 1.5 : style.width} strokeDasharray={style.dash ?? undefined} />
      {data.state === 'broken' && (
        <path className="ds-link__end" d={`M ${targetX - m} ${targetY - m} L ${targetX + m} ${targetY + m} M ${targetX + m} ${targetY - m} L ${targetX - m} ${targetY + m}`} strokeWidth={style.width} />
      )}
    </g>
  );
}

const NODE_TYPES = { tile: TileNode, card: CardNode, box: CardNode, ghost: GhostNode, unknown: UnknownNode, bundle: BundleNode, group: GroupNode, pod: PodNode };
const EDGE_TYPES = { ds: DsEdge };

// toFlow turns the adapter's model into React Flow nodes and edges.
export function toFlow(model, { sel, labelOf } = {}) {
  const nodes = model.nodes.map((n) => ({
    id: n.id,
    type: n.type,
    position: { x: n.x, y: n.y },
    data: n.data,
    width: n.w,
    height: n.h,
    parentId: n.parent,
    draggable: false,
    selectable: false,
    connectable: false,
    zIndex: n.type === 'group' ? 0 : n.parent ? 1 : undefined,
    // React Flow turns pointer events off on nodes without its own handlers; the map's
    // wrappers take the clicks, so they are turned on here (the node style is applied last).
    style: { width: n.w, height: n.h, pointerEvents: 'all' },
  }));
  const edges = model.edges.map((e) => ({
    id: e.id,
    type: 'ds',
    source: e.from,
    target: e.to,
    sourceHandle: e.sourceHandle ?? (e.from.startsWith('bundle:') ? 'out' : undefined),
    targetHandle: 'in',
    selectable: false,
    focusable: false,
    zIndex: 2,
    ariaLabel: labelOf ? labelOf(e) : linkAccessibleName(linkKind(e.kind), e.fromName, e.toName, stateWord(e.state), e.sentence || stateWord(e.state)),
    data: { state: e.state, selected: Boolean(sel && sel.t === 'row' && sel.id === e.rowId) },
  }));
  return { nodes, edges };
}

// Ready waits, frame by frame, until React Flow has measured every node (its internal
// nodes: the map is controlled, so the given nodes never carry the measurement), then fits the view
// and reports after two more animation frames.
function Ready({ onReady, fitKey }) {
  const rf = useReactFlow();
  const done = useRef(null);
  useEffect(() => {
    if (done.current === fitKey) {
      return undefined;
    }
    let id = 0;
    const check = () => {
      const nodes = rf.getNodes();
      const measured = (n) => {
        const internal = rf.getInternalNode(n.id);
        return Boolean(internal && internal.measured && internal.measured.width > 0);
      };
      if (nodes.every(measured)) {
        done.current = fitKey;
        rf.fitView({ padding: 0.08, maxZoom: 1 });
        id = requestAnimationFrame(() => {
          id = requestAnimationFrame(() => onReady && onReady());
        });
        return;
      }
      id = requestAnimationFrame(check);
    };
    id = requestAnimationFrame(check);
    return () => cancelAnimationFrame(id);
  }, [fitKey, onReady, rf]);
  return null;
}

// FlowMap draws one model. `fitKey` changes when the view should be fitted again; `onReady` is
// called once the nodes are measured, fitted and two animation frames have run.
export function FlowMap({ flow, onReady, fitKey, zoomRef }) {
  return (
    <ReactFlowProvider>
      <FlowInner flow={flow} onReady={onReady} fitKey={fitKey} zoomRef={zoomRef} />
    </ReactFlowProvider>
  );
}

function FlowInner({ flow, onReady, fitKey, zoomRef }) {
  const rf = useReactFlow();
  useEffect(() => {
    if (zoomRef) {
      zoomRef.current = rf;
    }
  }, [rf, zoomRef]);
  const proOptions = useMemo(() => ({ hideAttribution: true }), []);
  return (
    <ReactFlow
      nodes={flow.nodes}
      edges={flow.edges}
      nodeTypes={NODE_TYPES}
      edgeTypes={EDGE_TYPES}
      nodesDraggable={false}
      nodesConnectable={false}
      nodesFocusable={false}
      edgesFocusable={false}
      elementsSelectable={false}
      disableKeyboardA11y
      minZoom={0.05}
      maxZoom={2}
      proOptions={proOptions}
      onlyRenderVisibleElements={false}
    >
      <Ready onReady={onReady} fitKey={fitKey} />
    </ReactFlow>
  );
}
