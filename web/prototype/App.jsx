// The clickable prototype of the relation map (packet FA2-T8): the product layout of UI.md 2
// with the fixed data of 6.2, every screen state of 3.3 and the prototype-only control strip.
// The root element names the screen the state is (data-screen) and says when it is drawn
// and every on-demand file has arrived (data-ready).
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AttentionPanel,
  Banner,
  Breadcrumb,
  Chip,
  ClusterBandItem,
  ConnectionStateCard,
  EmptyState,
  LoadingProgress,
  TopBar,
  ZoomControl,
} from '../src/design-system/index.js';
import { run } from './controls.js';
import { DiagnosePanel, SettingsPanel } from './diagnose.jsx';
import { EmptyDetails, NodeDetails, OwnTabs, PlaceDetails, RowDetails } from './inspector.jsx';
import { FlowMap, MapContext, toFlow } from './map.jsx';
import { clusterModel, namespaceModel } from './measure/adapter.js';
import { NAMESPACES, cluster } from './data.js';
import { find } from './model.js';
import { BASE_ENV, ROW_ALIASES, SCREENS, buildScreen, reduce, screenIndex, screenOf } from './states.js';
import { DS, S, SESSION_LINES } from './strings.js';

// resolveRow maps a screen's row alias to the row id of the loaded data.
export function resolveRow(alias) {
  const a = ROW_ALIASES[alias];
  if (!a) {
    return alias;
  }
  const ns = NAMESPACES.get(alias.split(':')[0]);
  const card = ns.cards.get(a.from);
  const row = card.rows.find((r) => r.kind === a.kind && (!a.state || r.state === a.state));
  return row.id;
}

const INDEX = screenIndex(resolveRow);
const REPLACED = { 'cart-5b7d-k4': 'cart-5b7d-n3' };
const CONTEXTS = ['kind-intuitik-qa', 'kind-staging'];

function readable(state) {
  return state.env.listed ? cluster.namespaces.items : ['shop', ...state.opened];
}

function Strip({ dispatch }) {
  return (
    <nav className="proto-strip" data-prototype="strip" aria-label={S.strip}>
      <span className="proto-strip__title">{S.strip}</span>
      {Object.keys(SCREENS).map((id) => (
        <button key={id} type="button" className="proto-strip__button" data-control={`strip-${id}`} onClick={() => run(dispatch, `strip-${id}`)}>
          {S.stripShow(id)}
        </button>
      ))}
    </nav>
  );
}

function SessionScreen({ id }) {
  return (
    <main className="proto-session" data-session={id}>
      <div className="proto-session__card">
        {SESSION_LINES[id].map((line) => (
          <p key={line}>{line}</p>
        ))}
      </div>
    </main>
  );
}

function FindPanel({ state, dispatch }) {
  const [query, setQuery] = useState(state.find.query);
  const ref = useRef(null);
  useEffect(() => {
    if (ref.current) {
      ref.current.focus();
    }
  }, []);
  const hits = find(readable(state).map((n) => NAMESPACES.get(n)), state.find.query);
  return (
    <div className="proto-find" role="search">
      <input
        ref={ref}
        type="search"
        aria-label={S.findLabel}
        data-control="find-field"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          run(dispatch, 'find-field', e.target.value);
        }}
      />
      {state.find.query && hits.length === 0 && <p className="proto-find__none">{S.noMatches}</p>}
      {hits.length > 0 && (
        <ul className="proto-find__results" role="listbox" aria-label={S.findLabel}>
          {hits.map((h) => (
            <li key={h.uid}>
              <button type="button" className="proto-find__result" data-control="find-result" data-target={`${h.ns}/${h.kind}/${h.name}`} onClick={() => run(dispatch, 'find-result', { ns: h.ns, id: NAMESPACES.get(h.ns).top(h.uid).uid })}>
                <span className="proto-find__kind">{h.kind}</span> <span className="proto-find__name">{h.name}</span> <span className="proto-find__ns">{h.ns}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function attentionGroups(state) {
  const names = state.level === 'cluster' ? readable(state).filter((n) => n !== 'payments' || state.env.listed) : [state.ns];
  const groups = { broken: [], failing: [], warning: [], unknown: [], waiting: [] };
  for (const name of names) {
    const ns = NAMESPACES.get(name);
    if (!ns) {
      continue;
    }
    const g = ns.attention();
    for (const k of Object.keys(groups)) {
      groups[k].push(...g[k]);
    }
  }
  return groups;
}

function Attention({ state, dispatch }) {
  const groups = attentionGroups(state);
  const all = Object.values(groups).flat();
  const pick = (id) => {
    const item = all.find((i) => i.id === id);
    return { t: item.id.startsWith('u-') ? 'node' : 'row', id: item.id, ns: item.ns };
  };
  return (
    <aside className="proto-attention">
      {!state.env.listed && <p className="proto-note">{S.countingOnly}</p>}
      <AttentionPanel
        key={`${state.level}|${state.ns}`}
        groups={groups}
        selectedId={state.sel ? state.sel.id : undefined}
        updated={S.updated(state.env.updated)}
        footnote={S.optionalNote}
        onSelect={(id) => run(dispatch, 'needs-attention-row', pick(id))}
        onAsk={(id) => {
          const p = pick(id);
          run(dispatch, 'ask-why', { t: p.t, id: p.id });
        }}
      />
    </aside>
  );
}

function clusterLines(state) {
  const groups = attentionGroups(state);
  return [S.summaryCluster(readable(state).length), ...Object.entries(groups).filter(([, v]) => v.length > 0).map(([k, v]) => S.summaryCount(v.length, k))];
}

function namespaceLines(ns) {
  const kinds = new Map();
  for (const n of ns.answer.nodes) {
    kinds.set(n.kind, (kinds.get(n.kind) ?? 0) + 1);
  }
  return [...kinds.entries()].map(([k, v]) => S.summaryCount(v, k));
}

function Inspector({ state, dispatch, onPending }) {
  const ns = state.ns ? NAMESPACES.get(state.ns) : null;
  if (state.settings) {
    return <SettingsPanel state={state} dispatch={dispatch} />;
  }
  if (state.tab === 'diagnose') {
    return (
      <div className="ds-inspector proto-diagnose-shell">
        <OwnTabs dispatch={dispatch} tab="diagnose" />
        <div className="ds-inspector__body">
          <DiagnosePanel ns={ns} state={state} dispatch={dispatch} />
        </div>
      </div>
    );
  }
  const sel = state.sel;
  if (sel && ns && sel.t === 'node' && ns.byUid.has(sel.id)) {
    return <NodeDetails ns={ns} node={ns.byUid.get(sel.id)} state={state} dispatch={dispatch} onPending={onPending} />;
  }
  if (sel && ns && sel.t === 'row') {
    return <RowDetails ns={ns} row={ns.rowById(sel.id)} dispatch={dispatch} />;
  }
  if (sel && ns && (sel.t === 'ghost' || sel.t === 'unknown')) {
    const target = [...ns.cards.values()].flatMap((c) => c.rows).find((r) => r.target && r.target.id === sel.id).target;
    return <PlaceDetails ns={ns} target={target} dispatch={dispatch} />;
  }
  return <EmptyDetails lines={ns ? namespaceLines(ns) : clusterLines(state)} dispatch={dispatch} />;
}

function MapArea({ state, dispatch, onMapReady, mapKey }) {
  const zoomRef = useRef(null);
  const ns = state.ns ? NAMESPACES.get(state.ns) : null;
  const flow = useMemo(() => {
    if (state.env.phase !== 'ready') {
      if (state.env.phase === 'loading') {
        const first = ['shop', 'batch', 'media'].map((n) => NAMESPACES.get(n));
        return toFlow(clusterModel(first));
      }
      return null;
    }
    if (state.level === 'cluster') {
      const list = readable(state).map((n) => NAMESPACES.get(n));
      const model = clusterModel(list.filter((n) => n.name !== 'payments' || state.env.listed));
      if (!state.env.listed && state.opened.includes('payments')) {
        const last = model.nodes[model.nodes.length - 1];
        model.nodes.push({ ...last, id: 'tile:payments', x: last.x + 304, data: { name: 'payments', tile: NAMESPACES.get('payments').tile(), noPermission: true } });
      }
      return toFlow(model);
    }
    if (ns.empty) {
      return null;
    }
    return toFlow(namespaceModel(ns, { expanded: state.level === 'workload' ? state.workload : null }), { sel: state.sel });
  }, [state, ns]);
  const ctx = useMemo(
    () => ({
      ns,
      sel: state.sel,
      replaced: state.env.replaced ? REPLACED : null,
      onTile: (name) => run(dispatch, 'tile', name),
      onCard: (sel) => run(dispatch, 'card', sel),
      onRow: (id) => run(dispatch, 'card', { t: 'row', id }),
      onExpand: (uid) => run(dispatch, 'expand', uid),
    }),
    [ns, state.sel, state.env.replaced, dispatch],
  );
  const path = [{ stage: 'cluster', label: S.stage.cluster }];
  if (state.ns) {
    path.push({ stage: 'namespace', label: state.ns });
  }
  if (state.level === 'workload' && ns) {
    const w = ns.byUid.get(state.workload);
    path.push({ stage: 'workload', label: `${w.kind} ${w.name}` });
  }
  const refused = ns ? ns.answer.refused : [];
  return (
    <main className="proto-map">
      <div className="proto-map__bar">
        <Breadcrumb path={path} stage={state.level} onNavigate={(stage) => run(dispatch, 'breadcrumb', stage)} />
        <ZoomControl
          zoomInLabel={S.zoomIn}
          zoomOutLabel={S.zoomOut}
          onZoomIn={() => {
            zoomRef.current && zoomRef.current.zoomIn();
            run(dispatch, 'zoom-in');
          }}
          onZoomOut={() => {
            zoomRef.current && zoomRef.current.zoomOut();
            run(dispatch, 'zoom-out');
          }}
        />
      </div>
      {!state.env.listed && state.level === 'cluster' && (
        <Banner variant="permission" text={S.noList}>
          <label className="proto-field">
            {S.openNamespace}{' '}
            <input
              type="text"
              data-control="open-namespace"
              aria-label={S.openNamespace}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && e.currentTarget.value) {
                  run(dispatch, 'open-namespace', e.currentTarget.value.trim());
                }
              }}
            />
          </label>
        </Banner>
      )}
      {refused.length > 0 && (
        <div className="proto-partial">
          {refused.map((r) => (
            <Chip key={r.text} tone="unknown">
              {r.text}
            </Chip>
          ))}
          <button type="button" className="ds-button ds-button--secondary" data-control="retry-partial" onClick={() => run(dispatch, 'retry-partial')}>
            {DS.retry}
          </button>
        </div>
      )}
      <div className="proto-map__canvas">
        {state.env.phase === 'loading' && <LoadingProgress received={7} total={12} />}
        {state.env.phase === 'failed' && (
          <ConnectionStateCard state="broken" text={S.connectionFailure} onRetry={() => run(dispatch, 'retry-connection')} onChooseContext={() => run(dispatch, 'choose-another-context')} />
        )}
        {state.env.phase === 'ready' && ns && ns.empty && <EmptyState text={S.emptyDefault} />}
        {flow && (
          <MapContext.Provider value={state.env.phase === 'ready' ? ctx : { ns }}>
            <FlowMap key={mapKey} flow={flow} fitKey={mapKey} onReady={onMapReady} zoomRef={zoomRef} />
          </MapContext.Provider>
        )}
      </div>
      {state.level === 'cluster' && state.env.phase === 'ready' && (
        <div className="proto-band">
          <ClusterBandItem kind="Node" name="kind-intuitik-qa-control-plane" value={S.bandNodes(cluster.band.nodes.ready, cluster.band.nodes.total)} />
          {cluster.band.storageClasses.map((sc) => (
            <ClusterBandItem key={sc.name} kind="StorageClass" name={sc.name} value={sc.default ? S.bandDefault : undefined} />
          ))}
          <ClusterBandItem kind="PV" name="PersistentVolumes" value={S.bandPv(cluster.band.persistentVolumes.total)} />
        </div>
      )}
    </main>
  );
}

export default function App() {
  const [state, setState] = useState(() => buildScreen('P01', resolveRow));
  const [readyKey, setReadyKey] = useState(null);
  const [pending, setPending] = useState(0);
  const dispatch = useCallback((action) => {
    if (action[0] === 'screen') {
      setState(buildScreen(action[1], resolveRow));
      return;
    }
    setState((s) => reduce(s, action));
  }, []);
  const onPending = useCallback((n) => setPending((p) => p + n), []);
  const screen = screenOf(state, INDEX);
  const hasMap = !state.env.session && state.env.phase !== 'failed' && !(state.ns && NAMESPACES.get(state.ns).empty);
  const mapKey = `${state.env.phase}|${state.level}|${state.ns}|${state.workload}|${state.env.listed}|${state.opened.join(',')}|${state.env.replaced}`;
  const ready = (!hasMap || readyKey === mapKey) && pending === 0;
  const onMapReady = useCallback(() => setReadyKey(mapKey), [mapKey]);

  useEffect(() => {
    const onKey = (e) => {
      const typing = e.target && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
      if (e.key === 'Escape') {
        run(dispatch, 'esc');
      } else if (!typing && e.key === '/') {
        e.preventDefault();
        run(dispatch, 'find-field', '');
      } else if (!typing && e.key === '?') {
        run(dispatch, 'shortcut-list');
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [dispatch]);

  return (
    <div className="proto" data-screen={screen} data-ready={ready ? '1' : '0'} data-level={state.level} data-frame={state.frame ?? ''}>
      <Strip dispatch={dispatch} />
      {state.env.session ? (
        <SessionScreen id={state.env.session} />
      ) : (
        <div className="proto-app">
          <div className="proto-top">
            <TopBar
              contexts={CONTEXTS}
              context={CONTEXTS[0]}
              contextLabel={S.contextLabel}
              updated={S.updated(state.env.updated)}
              pausedSince={state.env.paused ? BASE_ENV.updated : undefined}
              onContextChange={() => run(dispatch, 'context-selector')}
              onFind={() => run(dispatch, 'find-field', '')}
              onSettings={() => run(dispatch, 'settings-link')}
              onAsk={() => run(dispatch, 'ask')}
            />
            <button type="button" className="ds-button ds-button--secondary proto-shortcuts-button" data-control="shortcut-list" aria-label={S.shortcutsLabel} onClick={() => run(dispatch, 'shortcut-list')}>
              {S.shortcuts}
            </button>
            {state.find && <FindPanel key="find" state={state} dispatch={dispatch} />}
            {state.shortcuts && (
              <ul className="proto-shortcuts" aria-label={S.shortcutsLabel}>
                {S.shortcutList.map((l) => (
                  <li key={l}>{l}</li>
                ))}
              </ul>
            )}
          </div>
          <div className="proto-body">
            <Attention state={state} dispatch={dispatch} />
            <MapArea state={state} dispatch={dispatch} onMapReady={onMapReady} mapKey={mapKey} />
            <aside className="proto-inspector">
              <Inspector state={state} dispatch={dispatch} onPending={onPending} />
            </aside>
          </div>
        </div>
      )}
    </div>
  );
}

