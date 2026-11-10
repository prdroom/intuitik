// C-20 to C-26: the panels.
import { useState } from 'react';
import { ATTENTION_GROUP_NAMES, STAGE_NAMES, STRINGS, updatesPaused } from '../strings.js';
import tokens, { linkGlyph } from '../tokens.js';
import { Button, Icon, StatePill } from './basics.jsx';
import { Port } from './map.jsx';

// The context choice of C-20: a button and a list of options. A native select is not used
// because WebKit's select inserts an inline style that the panel's CSP refuses.
function ContextPicker({ contexts, context, label, onChange }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="ds-context">
      <button type="button" className="ds-context__button" aria-haspopup="listbox" aria-expanded={open ? 'true' : 'false'} aria-label={label} onClick={() => setOpen(!open)}>
        <span className="ds-context__name">{context}</span>
        <span className="ds-context__caret" aria-hidden="true">
          {'\u25be'}
        </span>
      </button>
      {open && (
        <ul className="ds-context__list" role="listbox" aria-label={label}>
          {contexts.map((c) => (
            <li key={c} role="option" aria-selected={c === context ? 'true' : 'false'}>
              <button
                type="button"
                className="ds-context__option"
                onClick={() => {
                  setOpen(false);
                  if (onChange) {
                    onChange(c);
                  }
                }}
              >
                {c}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// C-20: context choice, the always visible Read-only mark, Find with its "/" shortcut, the
// update time or the paused band, Settings and Ask.
export function TopBar({ contexts = [], context, contextLabel, updated, pausedSince, onContextChange, onFind, onSettings, onAsk }) {
  return (
    <div className={`ds-topbar${pausedSince ? ' ds-topbar--paused' : ''}`}>
      <ContextPicker contexts={contexts} context={context} label={contextLabel} onChange={onContextChange} />
      <span className="ds-topbar__readonly">
        <Icon status="lock" />
        {STRINGS.readOnly}
      </span>
      <Button variant="secondary" onClick={onFind}>
        {STRINGS.find} <kbd className="ds-kbd">{STRINGS.findShortcut}</kbd>
      </Button>
      {pausedSince ? (
        <span className="ds-topbar__paused" role="status">
          <Icon status="clock" />
          {updatesPaused(pausedSince)}
        </span>
      ) : (
        <span className="ds-topbar__updated">{updated}</span>
      )}
      <span className="ds-topbar__spacer" />
      <Button variant="secondary" onClick={onSettings}>
        {STRINGS.settings}
      </Button>
      <Button variant="primary" onClick={onAsk}>
        {STRINGS.ask}
      </Button>
    </div>
  );
}

const STAGES = ['cluster', 'namespace', 'workload'];

// C-21: the path and the stage switch. A click on a path item or a stage emits
// onNavigate(stage); the current stage is marked.
export function Breadcrumb({ path = [], stage, onNavigate }) {
  return (
    <div className="ds-breadcrumb">
      <nav className="ds-breadcrumb__path">
        {path.map((p, i) => (
          <span key={p.stage} className="ds-breadcrumb__step">
            {i > 0 && (
              <span className="ds-breadcrumb__sep" aria-hidden="true">
                {'\u203a'}
              </span>
            )}
            <button type="button" className={`ds-breadcrumb__item${p.stage === stage ? ' ds-breadcrumb__item--current' : ''}`} aria-current={p.stage === stage ? 'page' : undefined} onClick={() => onNavigate && onNavigate(p.stage)}>
              {p.label}
            </button>
          </span>
        ))}
      </nav>
      <div className="ds-stage-switch" role="group">
        {STAGES.map((s) => (
          <button key={s} type="button" className={`ds-stage-switch__item${s === stage ? ' ds-stage-switch__item--current' : ''}`} aria-pressed={s === stage ? 'true' : 'false'} onClick={() => onNavigate && onNavigate(s)}>
            {STAGE_NAMES[s]}
          </button>
        ))}
      </div>
    </div>
  );
}

export const ATTENTION_ORDER = ['broken', 'failing', 'warning', 'unknown', 'waiting'];
const COLLAPSED_AT_START = new Set(['unknown', 'waiting']);
const GROUP_GLYPH = { broken: 'x', failing: 'x', warning: 'tri', unknown: 'lock', waiting: 'clock' };

// C-22 item: location / value / reason and "Ask why". Broken and Failing items carry the
// row background; a selected item has a border.
export function AttentionItem({ group, item, selected, onSelect, onAsk }) {
  return (
    <div className={`ds-attention-item ds-attention-item--${group}${selected ? ' ds-attention-item--selected' : ''}`} data-item-id={item.id}>
      <button type="button" className="ds-attention-item__select" aria-pressed={selected ? 'true' : 'false'} onClick={() => onSelect && onSelect(item.id)}>
        <span className="ds-attention-item__location">{item.location}</span>
        <span className="ds-attention-item__value">{item.value}</span>
        <span className="ds-attention-item__reason">
          <Icon status={GROUP_GLYPH[group]} />
          {item.reason}
        </span>
      </button>
      <Button variant="text-link" onClick={() => onAsk && onAsk(item.id)}>
        {STRINGS.askWhy}
      </Button>
    </div>
  );
}

// C-22: groups in the order Broken, Failing, Warning, Unknown, Waiting; Unknown and Waiting
// start collapsed. No items: "Nothing needs attention" and the caller's time.
export function AttentionPanel({ groups = {}, selectedId, updated, footnote, onSelect, onToggle, onAsk }) {
  const [open, setOpen] = useState(() => Object.fromEntries(ATTENTION_ORDER.map((g) => [g, !COLLAPSED_AT_START.has(g)])));
  const present = ATTENTION_ORDER.filter((g) => (groups[g] ?? []).length > 0);
  if (present.length === 0) {
    return (
      <div className="ds-attention ds-attention--empty">
        <Icon status="ok" />
        <span className="ds-attention__nothing">{STRINGS.nothingNeedsAttention}</span>
        <span className="ds-attention__time">{updated}</span>
      </div>
    );
  }
  const toggle = (g) => {
    const next = !open[g];
    setOpen({ ...open, [g]: next });
    if (onToggle) {
      onToggle(g, next);
    }
  };
  return (
    <div className="ds-attention">
      {present.map((g) => (
        <section key={g} className={`ds-attention-group ds-attention-group--${g}`} data-group={g}>
          <button type="button" className="ds-attention-group__head" aria-expanded={open[g] ? 'true' : 'false'} onClick={() => toggle(g)}>
            <Icon status={GROUP_GLYPH[g]} />
            <span className="ds-attention-group__name">{ATTENTION_GROUP_NAMES[g]}</span>
            <span className="ds-attention-group__count">{groups[g].length}</span>
          </button>
          {open[g] && (
            <div className="ds-attention-group__items">
              {groups[g].map((item) => (
                <AttentionItem key={item.id} group={g} item={item} selected={item.id === selectedId} onSelect={onSelect} onAsk={onAsk} />
              ))}
            </div>
          )}
        </section>
      ))}
      {footnote && <p className="ds-attention__footnote">{footnote}</p>}
    </div>
  );
}

export const LOG_KINDS = ['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob'];
const TABS = ['details', 'diagnose'];
const SUB_TABS = ['summary', 'yaml', 'events', 'logs'];

// C-23: Details and Diagnose tabs; the Details sub tabs Summary, YAML, Events and, only for
// Pods and workloads, Logs. Switching emits onTabChange or onSubTabChange.
export function InspectorShell({ kind, name, state, initialTab = 'details', initialSubTab = 'summary', onTabChange, onSubTabChange, children }) {
  const [tab, setTab] = useState(initialTab);
  const [subTab, setSubTab] = useState(initialSubTab);
  const subTabs = SUB_TABS.filter((t) => t !== 'logs' || LOG_KINDS.includes(kind));
  const label = { details: STRINGS.details, diagnose: STRINGS.diagnose, summary: STRINGS.summary, yaml: STRINGS.yaml, events: STRINGS.events, logs: STRINGS.logs };
  return (
    <div className="ds-inspector" data-kind={kind}>
      <div className="ds-inspector__head">
        <Icon kind={kind} />
        <span className="ds-inspector__kind">{kind}</span>
        <span className="ds-inspector__name">{name}</span>
        {state && <StatePill kind="node" state={state.state} text={state.text} />}
      </div>
      <div className="ds-tabs" role="tablist">
        {TABS.map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            className={`ds-tabs__tab${t === tab ? ' ds-tabs__tab--current' : ''}`}
            aria-selected={t === tab ? 'true' : 'false'}
            data-tab={t}
            onClick={() => {
              setTab(t);
              if (onTabChange) {
                onTabChange(t);
              }
            }}
          >
            {label[t]}
          </button>
        ))}
      </div>
      {tab === 'details' && (
        <div className="ds-tabs ds-tabs--sub" role="tablist">
          {subTabs.map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              className={`ds-tabs__tab${t === subTab ? ' ds-tabs__tab--current' : ''}`}
              aria-selected={t === subTab ? 'true' : 'false'}
              data-subtab={t}
              onClick={() => {
                setSubTab(t);
                if (onSubTabChange) {
                  onSubTabChange(t);
                }
              }}
            >
              {label[t]}
            </button>
          ))}
        </div>
      )}
      <div className="ds-inspector__body">{children}</div>
    </div>
  );
}

// C-24: the two ends of a link as mini cards joined by a line in the link's style, with the
// reference value beside the line. A missing target is drawn as a ghost.
export function LinkEnds({ from, link, to, ghost }) {
  const style = tokens.link[link.state];
  const glyph = linkGlyph(link.state);
  return (
    <div className="ds-link-ends">
      <div className="ds-mini-card">
        <Icon kind={from.kind} />
        <span className="ds-mini-card__kind">{from.kind}</span>
        <span className="ds-mini-card__name">{from.name}</span>
        <Port direction="out" state={link.state} />
      </div>
      <div className="ds-link-ends__joint">
        <svg className={`ds-link ds-link--${link.state}`} viewBox="0 0 16 40" width="16" height="40" aria-hidden="true" focusable="false">
          <path className="ds-link__line" d="M 8 0 L 8 40" strokeWidth={style.width} strokeDasharray={style.dash ?? undefined} />
        </svg>
        <span className="ds-link-ends__value">
          {glyph && <Icon status={glyph} />}
          {link.value}
        </span>
      </div>
      {ghost ? (
        <div className={`ds-mini-card ds-mini-card--ghost ds-mini-card--${ghost.variant ?? 'broken'}`}>
          <Port direction="in" state={link.state} />
          <span className="ds-mini-card__name">{ghost.target}</span>
          <span className="ds-mini-card__result">{ghost.result}</span>
        </div>
      ) : (
        <div className="ds-mini-card">
          <Port direction="in" state={link.state} />
          <Icon kind={to.kind} />
          <span className="ds-mini-card__kind">{to.kind}</span>
          <span className="ds-mini-card__name">{to.name}</span>
        </div>
      )}
    </div>
  );
}

// C-25: keys and monospace values.
export function DefinitionList({ items = [] }) {
  return (
    <dl className="ds-deflist">
      {items.map((it) => (
        <div key={it.key} className="ds-deflist__row">
          <dt>{it.key}</dt>
          <dd>{it.value}</dd>
        </div>
      ))}
    </dl>
  );
}

// C-26: + and - emit onZoomIn and onZoomOut; the accessible names come from the caller.
export function ZoomControl({ zoomInLabel, zoomOutLabel, onZoomIn, onZoomOut }) {
  return (
    <div className="ds-zoom" role="group">
      <Button variant="secondary" label={zoomInLabel} onClick={onZoomIn}>
        {STRINGS.zoomIn}
      </Button>
      <Button variant="secondary" label={zoomOutLabel} onClick={onZoomOut}>
        {STRINGS.zoomOut}
      </Button>
    </div>
  );
}
