// C-01 to C-05: Icon, Chip, StatePill, Button, CodeBlock with Copy.
import { useState } from 'react';
import { createCopyHandler } from '../copy.js';
import { LINK_STATE_NAMES, NODE_STATE_NAMES, STRINGS } from '../strings.js';
import { linkGlyph, nodeGlyph } from '../tokens.js';

// Icon outlines on a 16 x 16 grid, drawn with the current text colour.
const STATUS_PATHS = {
  x: 'M4.5 4.5 L11.5 11.5 M11.5 4.5 L4.5 11.5',
  clock: 'M8 2.5 A5.5 5.5 0 1 1 7.99 2.5 Z M8 5 V8 L10.2 9.4',
  tri: 'M8 2.5 L13.8 13 H2.2 Z M8 6.5 V9.5 M8 11.2 V11.4',
  lock: 'M4 7.5 H12 V13.5 H4 Z M5.8 7.5 V5.5 A2.2 2.2 0 0 1 10.2 5.5 V7.5',
  info: 'M8 2.5 A5.5 5.5 0 1 1 7.99 2.5 Z M8 7.2 V11.2 M8 5 V5.2',
  ok: 'M3.5 8.5 L6.5 11.5 L12.5 4.5',
  ring: 'M8 2.5 A5.5 5.5 0 1 1 7.99 2.5 Z',
};

const KIND_PATHS = {
  Ingress: 'M2 8 H9 M6.5 5.5 L9 8 L6.5 10.5 M10.5 3 H14 V13 H10.5',
  Service: 'M8 6 A2 2 0 1 1 7.99 6 Z M8 2 V6 M3 12.5 L6.3 9.2 M13 12.5 L9.7 9.2',
  Deployment: 'M2.5 5.5 H10.5 V13.5 H2.5 Z M5.5 5.5 V2.5 H13.5 V10.5 H10.5',
  StatefulSet: 'M2.5 3 H13.5 V6 H2.5 Z M2.5 6.5 H13.5 V9.5 H2.5 Z M2.5 10 H13.5 V13 H2.5 Z',
  DaemonSet: 'M3 3 H7 V7 H3 Z M9 3 H13 V7 H9 Z M3 9 H7 V13 H3 Z M9 9 H13 V13 H9 Z',
  CronJob: 'M2.5 2.5 H13.5 V13.5 H2.5 Z M8 4.5 V8 L10.5 9.5',
  Job: 'M2.5 2.5 H13.5 V13.5 H2.5 Z M5 8.5 L7.2 10.7 L11 6',
  Pod: 'M8 2 L13.2 5 V11 L8 14 L2.8 11 V5 Z',
  ConfigMap: 'M3.5 2 H10 L12.5 4.5 V14 H3.5 Z M5.5 7 H10.5 M5.5 9.5 H10.5 M5.5 12 H8.5',
  Secret: 'M5.5 7.5 A3 3 0 1 1 5.49 7.5 Z M8.5 8 H14 M12 8 V10.5 M13.8 8 V10',
  PVC: 'M3 4 A5 1.8 0 0 1 13 4 V12 A5 1.8 0 0 1 3 12 Z M3 4 A5 1.8 0 0 0 13 4 M6 9 H10',
  PV: 'M3 4 A5 1.8 0 0 1 13 4 V12 A5 1.8 0 0 1 3 12 Z M3 4 A5 1.8 0 0 0 13 4',
  StorageClass: 'M3 3.5 H13 V6.5 H3 Z M3 9.5 H13 V12.5 H3 Z M8 6.5 V9.5',
  HPA: 'M5 13 V3 M2.8 5.2 L5 3 L7.2 5.2 M11 3 V13 M8.8 10.8 L11 13 L13.2 10.8',
  HTTPRoute: 'M2 8 H7 L10 4.5 H14 M7 8 L10 11.5 H14 M12 2.5 L14 4.5 L12 6.5',
};

const GENERIC_PATH = 'M2.5 2.5 H13.5 V13.5 H2.5 Z';

export const STATUS_ICON_NAMES = Object.keys(STATUS_PATHS);
export const KIND_ICON_NAMES = Object.keys(KIND_PATHS);

// C-01: a status glyph (`status`) or a resource kind (`kind`). An unknown kind draws the
// generic box. Icons are decoration: the text next to them carries the meaning.
export function Icon({ status, kind }) {
  let name;
  let d;
  if (status) {
    name = STATUS_PATHS[status] ? status : 'generic';
    d = STATUS_PATHS[status] ?? GENERIC_PATH;
  } else {
    name = KIND_PATHS[kind] ? kind : 'generic';
    d = KIND_PATHS[kind] ?? GENERIC_PATH;
  }
  const dashed = status === 'ring';
  return (
    <svg className={`ds-icon ds-icon--${status ? 'status' : 'kind'}`} data-icon={name} viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
      <path d={d} strokeDasharray={dashed ? '2 2' : undefined} />
    </svg>
  );
}

// C-02: a 22 px chip; a tone takes its fg and bg token pair.
export function Chip({ tone = 'neutral', font = 'sans', children }) {
  return <span className={`ds-chip ds-chip--${tone} ds-chip--${font}`}>{children}</span>;
}

// C-03: glyph + state name + the caller's text. Connected and Healthy render nothing (DS-1).
export function StatePill({ kind = 'link', state, text }) {
  if (state === 'connected' || state === 'healthy') {
    return null;
  }
  const glyph = kind === 'node' ? nodeGlyph(state) : linkGlyph(state);
  const name = kind === 'node' ? NODE_STATE_NAMES[state] : LINK_STATE_NAMES[state];
  return (
    <span className={`ds-pill ds-pill--${kind} ds-pill--${state}`} data-state={state}>
      {glyph && <Icon status={glyph} />}
      <span className="ds-pill__name">{name}</span>
      {text && <span className="ds-pill__text">{text}</span>}
    </span>
  );
}

// C-04: one onClick per click; a disabled button stays focusable, ignores clicks and
// carries aria-disabled.
export function Button({ variant = 'secondary', disabled = false, onClick, label, children }) {
  const handleClick = (event) => {
    if (disabled) {
      if (event && event.preventDefault) {
        event.preventDefault();
      }
      return;
    }
    if (onClick) {
      onClick(event);
    }
  };
  return (
    <button type="button" className={`ds-button ds-button--${variant}`} aria-disabled={disabled ? 'true' : undefined} aria-label={label} onClick={handleClick}>
      {children}
    </button>
  );
}

// C-05: a monospace block and a Copy button. The copied text is the caller's text, byte for
// byte. The result labels come from the caller.
export function CodeBlock({ text, copiedText, failedText, writeText, onCopy }) {
  const [state, setState] = useState('idle');
  const copy = createCopyHandler({
    text,
    writeText,
    onResult: (result) => {
      setState(result);
      if (onCopy) {
        onCopy(result);
      }
    },
  });
  return (
    <div className="ds-code" data-copy-state={state}>
      <pre className="ds-code__text">
        <code>{text}</code>
      </pre>
      <div className="ds-code__bar">
        <Button variant="secondary" onClick={() => copy()}>
          {STRINGS.copy}
        </Button>
        <span className="ds-code__status" role="status">
          {state === 'copied' && copiedText}
          {state === 'failed' && failedText}
        </span>
      </div>
    </div>
  );
}
