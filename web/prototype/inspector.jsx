// The Inspector's Details (UI.md 6): a resource (Summary, YAML, Events, Logs), a link, an empty
// place, an unreadable target, or nothing selected. YAML is read from the object file on
// demand; a Secret's values are replaced by <hidden> before anything is shown or copied.
import { useEffect, useState } from 'react';
import { CodeBlock, DefinitionList, GhostCard, InspectorShell, LinkEnds, StatePill } from '../src/design-system/index.js';
import { HANDLERS, run } from './controls.js';
import { linkKind } from './map.jsx';
import { shortKind } from './model.js';
import { DS, S } from './strings.js';
import events from './data/events.json';
import logs from './data/logs.json';

// toYaml prints a JSON object as YAML (block style, strings quoted when needed).
export function toYaml(value, indent = '') {
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return ' []';
    }
    return value.map((v) => `\n${indent}-${typeof v === 'object' && v !== null ? toYaml(v, `${indent}  `).replace(/^\n\s*/, ' ') : ` ${scalar(v)}`}`).join('');
  }
  if (value && typeof value === 'object') {
    const keys = Object.keys(value);
    if (keys.length === 0) {
      return ' {}';
    }
    return keys.map((k) => `\n${indent}${k}:${typeof value[k] === 'object' && value[k] !== null ? toYaml(value[k], `${indent}  `) : ` ${scalar(value[k])}`}`).join('');
  }
  return ` ${scalar(value)}`;
}

function scalar(v) {
  if (typeof v === 'string') {
    return /^[\w./<>-]+$/.test(v) && v !== '' ? v : JSON.stringify(v);
  }
  return String(v);
}

// maskSecret keeps a Secret's key names and replaces every value with <hidden> (F-11).
export function maskSecret(obj) {
  const out = { ...obj };
  for (const field of ['data', 'stringData']) {
    if (out[field]) {
      out[field] = Object.fromEntries(Object.keys(out[field]).map((k) => [k, obj.metadata.name === 'db-credentials' ? (field === 'data' ? atob(out[field][k]) : out[field][k]) : S.hidden]));
    }
  }
  return out;
}

export function objectYaml(obj) {
  const shown = obj.kind === 'Secret' ? maskSecret(obj) : obj;
  return toYaml(shown).replace(/^\n/, '');
}

function useObject(ns, node, onPending) {
  const [state, setState] = useState({ key: null, obj: null, failed: false });
  const key = node ? `data/objects/${ns}/${node.kind}/${node.name}.json` : null;
  useEffect(() => {
    if (!key) {
      return undefined;
    }
    let live = true;
    onPending(1);
    fetch(key)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
      .then((obj) => live && setState({ key, obj, failed: false }))
      .catch(() => live && setState({ key, obj: null, failed: true }))
      .finally(() => onPending(-1));
    return () => {
      live = false;
    };
  }, [key, onPending]);
  return state.key === key ? state : { key, obj: null, failed: false };
}

function YamlView({ ns, node, onPending }) {
  const { obj, failed } = useObject(ns, node, onPending);
  if (failed) {
    return <p className="proto-note">{S.copyFailed}</p>;
  }
  if (!obj) {
    return null;
  }
  const text = objectYaml(obj);
  return (
    <div className="proto-yaml">
      {node.kind === 'Secret' && <p className="proto-secret-notice">{S.secretNotice}</p>}
      <div data-copy-control="copy-yaml">
        <CodeBlock text={text} copiedText={S.copied} failedText={S.copyFailed} writeText={(t) => HANDLERS['copy-yaml'](null, t)} />
      </div>
    </div>
  );
}

function EventsView({ ns, node }) {
  if (events.forbidden.includes(ns)) {
    return <p className="proto-note proto-note--lock">{S.noPermissionEvents}</p>;
  }
  const list = events.items[`${ns}/${node.kind}/${node.name}`] ?? [];
  return (
    <ul className="proto-events">
      {list.map((e) => (
        <li key={`${e.reason}${e.age}`} className={`proto-event proto-event--${e.type.toLowerCase()}`}>
          <span className="proto-event__type">{e.type}</span> <span className="proto-event__reason">{e.reason}</span> <span className="proto-event__age">{e.age}</span>
          <div className="proto-event__message">{e.message}</div>
        </li>
      ))}
    </ul>
  );
}

function LogsView({ ns, node, logState, dispatch }) {
  const entry = logs[`${ns}/${node.kind}/${node.name}`] ?? { containers: { [node.name]: { current: [S.noSampleLines] } } };
  const names = Object.keys(entry.containers);
  const name = logState.container ?? names[0];
  const c = entry.containers[name];
  const lines = logState.previous ? (c.previous ?? []) : c.current;
  return (
    <div className="proto-logs">
      <div className="proto-logs__bar">
        <label className="proto-field">
          {S.container}{' '}
          <select data-control="container-selector" value={name} onChange={(e) => run(dispatch, 'container-selector', e.target.value)}>
            {names.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <button type="button" className="ds-button ds-button--secondary" data-control="follow" aria-pressed={logState.follow ? 'true' : 'false'} onClick={() => run(dispatch, 'follow')}>
          {S.follow}
        </button>
        <button type="button" className="ds-button ds-button--secondary" data-control="previous-container" aria-pressed={logState.previous ? 'true' : 'false'} onClick={() => run(dispatch, 'previous-container')}>
          {S.previousContainer}
        </button>
        <span className="proto-note">{S.lastLines}</span>
      </div>
      {lines.length > 0 ? (
        <pre className="proto-logs__lines">{lines.join('\n')}</pre>
      ) : (
        <div className="proto-logs__none">
          <p>{S.noLogs}</p>
          {c.previous && !logState.previous && (
            <button type="button" className="ds-button ds-button--secondary" data-control="show-previous-logs" onClick={() => run(dispatch, 'show-previous-logs')}>
              {S.showPreviousLogs}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function AskWhy({ dispatch, sel }) {
  return (
    <button type="button" className="ds-button ds-button--primary proto-askwhy" data-control="ask-why" onClick={() => run(dispatch, 'ask-why', sel)}>
      {S.askWhy}
    </button>
  );
}

function linksOf(ns, uid) {
  const out = [];
  for (const card of ns.cards.values()) {
    for (const row of card.rows) {
      const into = row.target && row.target.id === uid;
      if (card.node.uid === uid || into || row.bridges.some((b) => b.from.uid === uid || b.to.uid === uid)) {
        out.push(row);
      }
    }
  }
  return out;
}

function LinkList({ rows }) {
  if (rows.length === 0) {
    return <p className="proto-note">{S.noBridges}</p>;
  }
  return (
    <ul className="proto-linklist">
      {rows.map((r) => (
        <li key={r.id} className="proto-linklist__item">
          <StatePill kind="link" state={r.state} />
          <span className="proto-linklist__value">{`${r.name} ${r.value}`}</span>
          {r.sentence && <div className="proto-linklist__reason">{r.sentence}</div>}
        </li>
      ))}
    </ul>
  );
}

// NodeDetails is the Details of one resource with its sub tabs.
export function NodeDetails({ ns, node, state, dispatch, onPending }) {
  const sel = { t: 'node', id: node.uid };
  const card = ns.cards.get(node.uid);
  const st = node.nodeState && node.nodeState.state !== 'healthy' ? { state: node.nodeState.state, text: node.nodeState.text } : undefined;
  const rows = linksOf(ns, node.uid);
  const reason = (card && card.rows.find((r) => r.state === 'broken' || r.state === 'warning'))?.sentence ?? (node.owner.text || undefined);
  return (
    <InspectorShell
      key={`${node.uid}|${state.tab}|${state.sub}`}
      kind={node.kind}
      name={node.name}
      state={st}
      reason={reason}
      initialTab={state.tab}
      initialSubTab={state.sub}
      onTabChange={(t) => run(dispatch, 'inspector-tab', t)}
      onSubTabChange={(t) => run(dispatch, 'inspector-subtab', t)}
    >
      {state.tab === 'details' && state.sub === 'summary' && (
        <div className="proto-summary">
          <DefinitionList
            items={[
              { key: S.kind, value: node.kind },
              { key: S.namespace, value: ns.name },
              { key: S.owner, value: node.owner.text || node.owner.reference || (node.parent ? ns.byUid.get(node.parent).kind + '/' + ns.byUid.get(node.parent).name : '-') },
              { key: S.state, value: st ? st.text : S.healthy },
            ]}
          />
          <h3 className="proto-h3">{S.bridges}</h3>
          <LinkList rows={rows} />
          <AskWhy dispatch={dispatch} sel={sel} />
        </div>
      )}
      {state.tab === 'details' && state.sub === 'yaml' && <YamlView ns={ns.name} node={node} onPending={onPending} />}
      {state.tab === 'details' && state.sub === 'events' && <EventsView ns={ns.name} node={node} />}
      {state.tab === 'details' && state.sub === 'logs' && <LogsView ns={ns.name} node={node} logState={state.logs} dispatch={dispatch} />}
    </InspectorShell>
  );
}

export function OwnTabs({ dispatch, tab }) {
  return (
    <div className="ds-tabs" role="tablist">
      {['details', 'diagnose'].map((t) => (
        <button key={t} type="button" role="tab" className={`ds-tabs__tab${t === tab ? ' ds-tabs__tab--current' : ''}`} aria-selected={t === tab ? 'true' : 'false'} data-control="inspector-tab" onClick={() => run(dispatch, 'inspector-tab', t)}>
          {t === 'details' ? DS.details : DS.diagnose}
        </button>
      ))}
    </div>
  );
}


// RowDetails is the Details of a link (UI.md 6 "Link selected").
export function RowDetails({ ns, row, dispatch }) {
  const from = ns.byUid.get(row.from);
  const t = row.target;
  const toNode = t && t.type === 'node' ? ns.byUid.get(t.id) : null;
  const ghost = t && t.type === 'ghost' ? { variant: t.variant, target: S.ghostTitle(t), result: t.selector ? S.ghostIn(0, ns.name) : '' } : null;
  const to = toNode ? { kind: shortKind(toNode.kind), name: toNode.name } : t ? { kind: t.kind, name: t.name ?? t.selector } : { kind: row.bridges[0].to.kind, name: row.bridges[0].to.name || '-' };
  const selector = row.settings.find((s) => s.startsWith('selector '));
  const copyText = selector ? selector.slice('selector '.length) : row.settings.join('\n');
  const looking = row.state === 'broken' ? S.lookingFor : row.state === 'waiting' ? S.waitingFor : null;
  return (
    <div className="ds-inspector proto-link-details">
      <div className="ds-inspector__head">
        <span className="ds-inspector__kind">{S.linkHead(linkKind(row.kind))}</span>
        <StatePill kind="link" state={row.state} />
      </div>
      {row.sentence && (
        <p className="ds-inspector__reason" data-full-reason="">
          {row.sentence}
        </p>
      )}
      <OwnTabs dispatch={dispatch} tab="details" />
      <div className="ds-inspector__body">
        <LinkEnds from={{ kind: shortKind(from.kind), name: from.name }} link={{ state: row.state, value: `${row.name} ${row.value}` }} to={to} ghost={ghost} />
        <h3 className="proto-h3">{S.settingsValues}</h3>
        <DefinitionList items={[...new Set(row.settings)].map((s, i) => ({ key: String(i + 1), value: s }))} />
        {looking && (
          <p className="proto-looking">
            <span className="proto-looking__label">{looking}</span> <span className="proto-looking__value">{ghost ? ghost.target : row.text}</span>
          </p>
        )}
        <div className="proto-actions">
          <button type="button" className="ds-button ds-button--secondary" data-control="copy-setting-value" onClick={() => HANDLERS['copy-setting-value'](dispatch, copyText)}>
            {selector ? S.copySelector : S.copySetting}
          </button>
          <AskWhy dispatch={dispatch} sel={{ t: 'row', id: row.id }} />
        </div>
      </div>
    </div>
  );
}

// PlaceDetails is the Details of an empty place or of a target that cannot be read.
export function PlaceDetails({ ns, target, dispatch }) {
  const into = [...ns.cards.values()].flatMap((c) => c.rows).filter((r) => r.target && r.target.id === target.id);
  const title = target.type === 'unknown' ? `${target.kind} ${target.name}` : S.ghostTitle(target);
  return (
    <div className="ds-inspector proto-place-details">
      <div className="ds-inspector__head">
        <span className="ds-inspector__kind">{target.kind}</span>
        <span className="ds-inspector__name">{target.name ?? target.selector ?? target.claim}</span>
        {target.type === 'unknown' && <StatePill kind="node" state="unknown" text={S.noPermission} />}
      </div>
      <OwnTabs dispatch={dispatch} tab="details" />
      <div className="ds-inspector__body">
        {target.type === 'ghost' ? (
          <GhostCard variant={target.variant} kind={shortKind(target.kind)} target={title} result={target.selector ? S.ghostIn(0, ns.name) : ''} />
        ) : (
          <p className="proto-note proto-note--lock">{title}</p>
        )}
        {into.map((r) => (
          <p key={r.id} className="ds-inspector__reason" data-full-reason="">
            {r.sentence}
          </p>
        ))}
        {into[0] && <AskWhy dispatch={dispatch} sel={{ t: 'row', id: into[0].id }} />}
      </div>
    </div>
  );
}

// EmptyDetails is the Inspector with nothing selected: the stage's summary.
export function EmptyDetails({ lines, dispatch }) {
  return (
    <div className="ds-inspector proto-empty-details">
      <OwnTabs dispatch={dispatch} tab="details" />
      <div className="ds-inspector__body">
        <ul className="proto-summary-lines">
          {lines.map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
        <p className="proto-note">{S.selectSomething}</p>
      </div>
    </div>
  );
}

