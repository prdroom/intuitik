// The Diagnose tab (UI.md 7) and Settings. The provider is mocked: Send and Test connection
// answer from the prototype's fixed text and send no request. What would be sent is always
// shown under the question and is recounted from the current selection on every change.
import { useState } from 'react';
import { Chip, CodeBlock, ConsentCard, ErrorCard, Icon, NotSetUpCard, ReviewPanel, SendSummary, StatePill } from '../src/design-system/index.js';
import { HANDLERS, run } from './controls.js';
import { DS, S } from './strings.js';
import events from './data/events.json';
import sample from './data/diagnose.json';

const LOG_LINES = 200;

// contextOf is what Ask why sends about an element: the element and the nodes its links join
// (UI.md 7: the element and its adjacent links and nodes). Secrets appear with key names only.
export function contextOf(ns, about) {
  if (!ns || !about) {
    return [];
  }
  const out = new Map();
  const add = (uid) => {
    const n = ns.byUid.get(uid);
    if (n && !out.has(uid)) {
      out.set(uid, n);
    }
  };
  let rows = [];
  if (about.t === 'row') {
    const row = ns.rowById(about.id);
    add(row.from);
    if (row.target && row.target.type === 'node') {
      add(row.target.id);
    }
    rows = [row];
  } else {
    const top = ns.top(about.id) ?? ns.byUid.get(about.id);
    if (top) {
      add(about.id);
      add(top.uid);
      for (const card of ns.cards.values()) {
        for (const row of card.rows) {
          if (card.node.uid === top.uid || (row.target && row.target.id === top.uid)) {
            add(card.node.uid);
            if (row.target && row.target.type === 'node') {
              add(row.target.id);
            }
            rows.push(row);
          }
        }
      }
    }
  }
  return [...out.values()].map((n) => ({ id: n.uid, label: `${n.kind} ${n.name}`, node: n, rows: rows.filter((r) => r.from === n.uid || (r.target && r.target.id === n.uid)) }));
}

// previewLine is the resource summary sent for one resource: kind, name, state and the links'
// settings and states. It is built from the graph answer, which has no Secret value.
function previewLine(ns, r) {
  const st = r.node.nodeState && r.node.nodeState.text ? ` \u00b7 ${r.node.nodeState.text}` : '';
  const links = r.rows.map((row) => `${row.name} ${row.value}${row.sentence ? ` (${row.sentence})` : ''}`).join('; ');
  return `${r.label}${st}${links ? ` \u00b7 ${links}` : ''}`;
}

function counts(ns, resources, d) {
  const included = resources.filter((r) => !d.excluded.includes(r.id));
  const ev = included.reduce((a, r) => a + (events.items[`${ns.name}/${r.node.kind}/${r.node.name}`] ?? []).length, 0);
  const withLogs = included.filter((r) => ['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob'].includes(r.node.kind)).length;
  return {
    included,
    resources: d.cats.summaries ? included.length : 0,
    events: d.cats.events ? ev : 0,
    logLines: d.cats.logs ? withLogs * LOG_LINES : 0,
  };
}

function Compose({ ns, d, state, dispatch }) {
  const resources = contextOf(ns, d.about);
  const c = counts(ns, resources, d);
  const local = state.env.llm === 'local-down';
  const aboutLabel = resources[0] ? resources[0].label : '';
  const initial = d.about && d.about.id === 'u-shop-service-web' ? S.questionServiceWeb : resources[0] ? S.questionFor(resources[0].node.kind, resources[0].node.name) : '';
  const [question, setQuestion] = useState(initial);
  return (
    <div className="proto-compose">
      {aboutLabel && (
        <p className="proto-about">
          <span className="proto-about__label">{S.about}</span> <StatePill kind="link" state="broken" /> <span>{aboutLabel}</span>
        </p>
      )}
      <label className="proto-field proto-field--block">
        {S.question}
        <textarea data-control="question-field" rows={2} value={question} onChange={(e) => { setQuestion(e.target.value); run(dispatch, 'question-field', e.target.value); }} />
      </label>
      <SendSummary provider={local ? S.sendToLocal : S.sendTo} resources={c.resources} events={c.events} logLines={c.logLines} />
      <div className="proto-review">
        <h3 className="proto-h3">{S.review}</h3>
        <ReviewPanel
          key={d.excluded.join(',')}
          initial={d.cats}
          resources={resources.filter((r) => !d.excluded.includes(r.id)).map((r) => ({ id: r.id, label: r.label }))}
          excludeLabel={S.excludeLabel}
          onChange={(sel) => {
            for (const cat of ['summaries', 'events', 'logs']) {
              if (sel.categories[cat] !== d.cats[cat]) {
                run(dispatch, 'review-checkbox', { cat, on: sel.categories[cat] });
              }
            }
            for (const id of sel.excluded) {
              if (!d.excluded.includes(id)) {
                run(dispatch, 'review-remove', id);
              }
            }
          }}
        />
        <div className="proto-preview" data-send-preview="">
          <h3 className="proto-h3">{S.sendPreview}</h3>
          {d.cats.summaries && (
            <ul className="proto-preview__list">
              {c.included.map((r) => (
                <li key={r.id}>{previewLine(ns, r)}</li>
              ))}
            </ul>
          )}
        </div>
      </div>
      <div className="proto-actions">
        <button type="button" className="ds-button ds-button--primary" data-control="send" onClick={() => run(dispatch, 'send')}>
          {S.send}
        </button>
      </div>
      <p className="proto-note">{S.conversationsNotSaved}</p>
    </div>
  );
}

function Answer({ d, dispatch }) {
  const a = sample.answer;
  return (
    <div className="ds-answer proto-answer">
      <p className="proto-note">{S.sampleAnswer}</p>
      <div className="ds-answer__cap">{DS.likelyCause}</div>
      <p className="ds-answer__cause">{a.likelyCause}</p>
      <p className="ds-answer__text">{a.explanation}</p>
      <div className="ds-answer__cap">{DS.evidence}</div>
      <div className="ds-answer__chips">
        {a.evidence.map((e) => (
          <span key={e.label} className={`ds-evidence ds-evidence--${e.state}`}>
            {e.label}
          </span>
        ))}
      </div>
      <div className="ds-answer__cap">{DS.fix}</div>
      <p className="ds-answer__note">{DS.runThisYourself}</p>
      <div data-copy-control="copy-fix">
        <CodeBlock text={a.fix} copiedText={S.copied} failedText={S.copyFailed} writeText={(t) => HANDLERS['copy-fix'](null, t)} />
      </div>
      <div className="ds-answer__cap">{DS.docs}</div>
      <ul className="ds-answer__docs">
        {a.docs.map((t) => (
          <li key={t}>{t}</li>
        ))}
      </ul>
      <div className="ds-answer__chips">
        {d.withoutLogs && <Chip>{S.withoutLogs}</Chip>}
        <Chip>{S.docsVersion}</Chip>
      </div>
      <div className="ds-answer__attach">
        <span>{d.attached ? S.attached : S.attachHint}</span>
        <button type="button" className="ds-button ds-button--secondary" data-control="attach" onClick={() => run(dispatch, 'attach')}>
          {DS.attach}
        </button>
      </div>
    </div>
  );
}

export function DiagnosePanel({ ns, state, dispatch }) {
  const d = state.diag;
  if (state.env.llm === 'none') {
    return <NotSetUpCard text={S.notConfigured} onOpenSettings={() => run(dispatch, 'settings-link')} />;
  }
  return (
    <div className="proto-diagnose">
      <Compose key={JSON.stringify(d.about)} ns={ns} d={d} state={state} dispatch={dispatch} />
      {d.view === 'consent' && <ConsentCard scope={S.consentScope} onLogDefaultChange={(v) => run(dispatch, 'log-default', v)} onAgree={() => run(dispatch, 'agree-and-send')} onDecline={() => run(dispatch, 'dont-send')} />}
      {d.view === 'declined' && (
        <div className="ds-card-panel proto-declined">
          <p>
            <Icon status="info" />
            {S.useLocal}
          </p>
          <button type="button" className="ds-button ds-button--secondary" data-control="settings-link" onClick={() => run(dispatch, 'settings-link')}>
            {DS.settings}
          </button>
        </div>
      )}
      {d.view === 'error' && <ErrorCard text={S[d.error]} onRetry={() => run(dispatch, 'retry-error-card')} />}
      {d.view === 'answer' && <Answer d={d} dispatch={dispatch} />}
    </div>
  );
}

export function SettingsPanel({ state, dispatch }) {
  const s = state.settings;
  const [typed, setTyped] = useState('');
  return (
    <div className="ds-inspector proto-settings">
      <div className="ds-inspector__head">
        <span className="ds-inspector__name">{S.settingsTitle}</span>
      </div>
      <div className="ds-inspector__body">
        <label className="proto-field">
          {S.provider}{' '}
          <select data-control="provider-field" value={s.provider} onChange={(e) => run(dispatch, 'provider-field', e.target.value)}>
            {S.providers.map((p) => (
              <option key={p}>{p}</option>
            ))}
          </select>
        </label>
        <label className="proto-field">
          {S.model}{' '}
          <select data-control="model-field" value={s.model} onChange={(e) => run(dispatch, 'model-field', e.target.value)}>
            {S.models.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </label>
        <label className="proto-field proto-field--block">
          {S.apiKey}
          <span className="proto-key-mask">{s.key === 'none' ? '' : s.save === 'session' ? S.keySession : S.keyMasked}</span>
          <input type="password" autoComplete="off" data-control="api-key-field" value={typed} onChange={(e) => { setTyped(e.target.value); }} onBlur={() => run(dispatch, 'api-key-field', typed)} />
        </label>
        <div className="proto-radios" role="radiogroup">
          <label>
            <input type="radio" name="proto-save" data-control="save-key-on-computer" checked={s.save === 'computer'} onChange={() => run(dispatch, 'save-key-on-computer')} />
            {S.saveOnComputer}
          </label>
          <label>
            <input type="radio" name="proto-save" data-control="use-for-session" checked={s.save === 'session'} onChange={() => run(dispatch, 'use-for-session')} />
            {S.sessionOnly}
          </label>
        </div>
        <p className="proto-note">{S.savePath}</p>
        <div className="proto-actions">
          <button type="button" className="ds-button ds-button--secondary" data-control="test-connection" onClick={() => run(dispatch, 'test-connection')}>
            {S.testConnection}
          </button>
          {s.test === 'ok' && <span className="proto-result proto-result--ok">{S.connectedModel}</span>}
          {s.test === 'auth' && <span className="proto-result proto-result--bad">{S.errAuth}</span>}
        </div>
        <div className="proto-actions">
          <button type="button" className="ds-button ds-button--secondary" data-control="delete-saved-key" onClick={() => run(dispatch, 'delete-saved-key')}>
            {S.deleteKey}
          </button>
          {s.del === 'ok' && <span className="proto-result proto-result--ok">{S.deleted}</span>}
          {s.del === 'failed' && <span className="proto-result proto-result--bad">{S.couldntDelete}</span>}
        </div>
        <div className="proto-radios" role="radiogroup">
          <span>{S.logDefault}</span>
          <label>
            <input type="radio" name="proto-logs" data-control="log-default" checked={s.logDefault === 'include'} onChange={() => run(dispatch, 'log-default', 'include')} />
            {DS.includeLogsByDefault}
          </label>
          <label>
            <input type="radio" name="proto-logs" data-control="log-default" checked={s.logDefault === 'exclude'} onChange={() => run(dispatch, 'log-default', 'exclude')} />
            {DS.excludeLogsByDefault}
          </label>
        </div>
        <h3 className="proto-h3">{S.dataOnComputer}</h3>
        <p className="proto-note">{S.dataPaths}</p>
        <p className="proto-note">{S.resetHint}</p>
        <p className="proto-note">{S.conversationsNotSaved}</p>
      </div>
    </div>
  );
}
