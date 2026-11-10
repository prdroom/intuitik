// C-10 to C-17: the map elements.
import { useLayoutEffect, useRef } from 'react';
import { inPortCenter, rowCenters } from '../geometry.js';
import { cardReason } from '../reason.js';
import { LINK_STATE_NAMES, foldedConnected, linkAccessibleName, noResourcesIn, withBrokenLinks } from '../strings.js';
import tokens, { linkGlyph, nodeGlyph } from '../tokens.js';
import { Chip, Icon } from './basics.jsx';

const TAB_BORDER_CLASS = { failing: 'ds-tab-border-solid', progressing: 'ds-tab-border-dashed', unknown: 'ds-tab-border-solid' };
const TAB_FILL_CLASS = { failing: 'ds-tab-fill-failing', progressing: 'ds-tab-fill-progressing', unknown: 'ds-tab-fill-unknown' };

// C-10: the node-state tab on the card's top edge. Healthy renders nothing. A node state
// with broken links appends " \u00b7 N broken link". Attention (broken-link propagation) is not
// a tab: the \u2715 mark and the caller's text with no tab border and no fill (DS-D).
export function StatusTab({ state, text, brokenLinks = 0 }) {
  if (state === 'healthy' || !state) {
    return null;
  }
  if (state === 'attention') {
    return (
      <div className="ds-status-mark ds-status-mark--attention" data-status="attention">
        <Icon status={nodeGlyph('attention')} />
        <span className="ds-status-mark__text">{text}</span>
      </div>
    );
  }
  const label = brokenLinks > 0 ? withBrokenLinks(text, brokenLinks) : text;
  return (
    <div className={`ds-status-tab ds-status-tab--${state} ${TAB_BORDER_CLASS[state]} ${TAB_FILL_CLASS[state]}`} data-status={state}>
      <Icon status={nodeGlyph(state)} />
      <span className="ds-status-tab__text">{label}</span>
    </div>
  );
}

// C-12: a port. Out ports are filled with the state colour (Connected: white); in ports
// are white with the state colour as their ring.
export function Port({ direction = 'out', state = 'connected' }) {
  return <span className={`ds-port ds-port--${direction} ds-port--${state}`} data-port={direction} aria-hidden="true" />;
}

// C-11: one reference row. Connected is one line; any other state with a reason is two
// lines. Only Broken and Warning get a background. The value may be cut with an ellipsis
// by CSS, its full text stays in the DOM and in its title. The reason is the caller's
// dictionary sentence, shown as its card sentence (cardReason); it wraps and is never cut.
export function RefRow({ id, state = 'connected', name, value, fact, reason, full = false, port = true }) {
  const twoLine = state !== 'connected' && Boolean(reason);
  const glyph = linkGlyph(state);
  return (
    <div className={`ds-refrow ds-refrow--${state}${twoLine ? ' ds-refrow--two-line' : ''}${full ? ' ds-refrow--full' : ''}`} data-row-id={id} data-row-state={state}>
      <div className="ds-refrow__line">
        {glyph ? <Icon status={glyph} /> : <span className="ds-refrow__noglyph" aria-hidden="true" />}
        <span className="ds-refrow__name">{name}</span>
        <span className="ds-refrow__value" title={value}>
          {value}
        </span>
        {fact && <span className="ds-refrow__fact">{fact}</span>}
      </div>
      {twoLine && <div className="ds-refrow__reason">{cardReason(reason)}</div>}
      {port && <Port direction="out" state={state} />}
    </div>
  );
}

// C-13: a link between two points. Width and dash come from tokens.link; Broken ends with
// a \u2715 mark. The accessible name names the kind, both ends, the state and the caller's text.
export function Link({ from, to, state = 'connected', kind, fromLabel, toLabel, text, width, height }) {
  const style = tokens.link[state];
  const dx = Math.max(24, (to.x - from.x) / 2);
  const d = `M ${from.x} ${from.y} C ${from.x + dx} ${from.y}, ${to.x - dx} ${to.y}, ${to.x} ${to.y}`;
  const m = 4;
  const label = linkAccessibleName(kind, fromLabel, toLabel, LINK_STATE_NAMES[state], text);
  return (
    <svg className={`ds-link ds-link--${state}`} role="img" aria-label={label} width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      <path className="ds-link__line" d={d} strokeWidth={style.width} strokeDasharray={style.dash ?? undefined} />
      {state === 'broken' && (
        <path className="ds-link__end" d={`M ${to.x - m} ${to.y - m} L ${to.x + m} ${to.y + m} M ${to.x + m} ${to.y - m} L ${to.x - m} ${to.y + m}`} strokeWidth={style.width} />
      )}
    </svg>
  );
}

// The fold rule of C-14: in compact mode, more than two normal (Connected) rows fold into
// one "+N connected" row with one bundle port; anomalous rows never fold.
export function foldRows(rows, mode) {
  const normal = rows.filter((r) => r.state === 'connected');
  if (mode !== 'compact' || normal.length <= 2) {
    return rows.map((r) => ({ type: 'row', row: r }));
  }
  const kept = new Set(normal.slice(0, 2));
  const folded = normal.length - 2;
  const out = [];
  let foldPlaced = false;
  for (const r of rows) {
    if (r.state !== 'connected' || kept.has(r)) {
      out.push({ type: 'row', row: r });
    } else if (!foldPlaced) {
      out.push({ type: 'fold', count: folded });
      foldPlaced = true;
    }
  }
  return out;
}

const WORKLOAD_ROW_NAME = 'ds-card__rows';

// C-14: the node card. Rows report their port centres to the parent through
// onPortCentres({ in, out: { rowId: y } }), measured from the laid-out rows (geometry.js),
// again whenever a row's size changes (fonts loading, expanding, folding).
export function NodeCard({ kind, name, rows = [], chips = [], status, mode = 'compact', selected = false, owned, onPortCentres }) {
  const cardRef = useRef(null);
  const reportedRef = useRef(null);
  const visible = foldRows(rows, mode);
  const unknown = status && status.state === 'unknown';

  useLayoutEffect(() => {
    const card = cardRef.current;
    if (!onPortCentres || !card) {
      return undefined;
    }
    const container = card.querySelector(`.${WORKLOAD_ROW_NAME}`);
    const measure = () => {
      const rowEls = container ? Array.from(container.children).filter((el) => el.dataset.rowId) : [];
      const heights = rowEls.map((el) => el.getBoundingClientRect().height);
      const centres = rowCenters(heights);
      const out = {};
      rowEls.forEach((el, i) => {
        out[el.dataset.rowId] = centres[i];
      });
      const report = { in: inPortCenter(), out };
      const key = JSON.stringify(report);
      if (key !== reportedRef.current) {
        reportedRef.current = key;
        onPortCentres(report);
      }
    };
    measure();
    if (typeof ResizeObserver === 'undefined') {
      return undefined;
    }
    const observer = new ResizeObserver(measure);
    observer.observe(card);
    if (container) {
      Array.from(container.children).forEach((el) => observer.observe(el));
    }
    return () => observer.disconnect();
  });

  return (
    <div ref={cardRef} className={`ds-card ds-card--${mode}${selected ? ' ds-card--selected' : ''}${unknown ? ' ds-card--unknown' : ''}`} data-card-mode={mode}>
      {status && <StatusTab state={status.state} text={status.text} brokenLinks={status.brokenLinks} />}
      <div className="ds-card__header">
        <Port direction="in" state="connected" />
        <Icon kind={kind} />
        <span className="ds-card__kind">{kind}</span>
        <span className="ds-card__name">{name}</span>
      </div>
      {visible.length > 0 && (
        <div className={WORKLOAD_ROW_NAME}>
          {visible.map((v) =>
            v.type === 'fold' ? (
              <div key="fold" className="ds-refrow ds-refrow--connected ds-refrow--fold" data-row-id="fold" data-row-state="connected">
                <div className="ds-refrow__line">
                  <span className="ds-refrow__noglyph" aria-hidden="true" />
                  <span className="ds-refrow__name">{foldedConnected(v.count)}</span>
                </div>
                <Port direction="out" state="connected" />
              </div>
            ) : (
              <RefRow key={v.row.id} {...v.row} full={mode === 'expanded'} />
            ),
          )}
        </div>
      )}
      {mode === 'expanded' && owned && (
        <div className="ds-card__owned">
          <div className="ds-card__owner">
            <Icon kind={owned.kind} />
            <span className="ds-card__kind">{owned.kind}</span>
            <span className="ds-card__name">{owned.name}</span>
          </div>
          {owned.items.map((item) => (
            <div key={item.id} className={`ds-owned-row ds-owned-row--${item.state ?? 'healthy'}`} data-owned-id={item.id}>
              <Port direction="in" state="connected" />
              <Icon kind={item.kind} />
              <span className="ds-owned-row__name">{item.name}</span>
              {item.note && <span className="ds-owned-row__note">{item.note}</span>}
            </div>
          ))}
        </div>
      )}
      {chips.length > 0 && (
        <div className="ds-card__footer">
          {chips.map((c) => (
            <Chip key={c.text} tone={c.tone} font={c.font ?? 'sans'}>
              {c.text}
            </Chip>
          ))}
        </div>
      )}
    </div>
  );
}

// C-15: the empty place of a missing target: dashed outline, hatched fill, what is looked
// for, the result and the explanation. Optional targets use the optional colour.
export function GhostCard({ variant = 'broken', kind, target, result, description }) {
  return (
    <div className={`ds-ghost ds-ghost--${variant}`}>
      <Port direction="in" state={variant} />
      <div className="ds-ghost__head">
        <Icon kind={kind} />
        <span className="ds-ghost__target">{target}</span>
      </div>
      <div className="ds-ghost__result">{result}</div>
      {description && <div className="ds-ghost__description">{description}</div>}
    </div>
  );
}

const POD_KINDS = ['ok', 'progressing', 'failing'];

// C-16: a Namespace tile. All numbers are shown as given (the component sums nothing).
// Broken and failing counts are tabs, Pod states a bar, the other anomalies chips.
export function NamespaceTile({ name, workloads, pods = {}, anomalies = {}, labels = {}, noPermission }) {
  const podTotalGiven = POD_KINDS.some((k) => (pods[k] ?? 0) > 0);
  const empty = !noPermission && !podTotalGiven;
  const tabs = ['broken', 'failing'].filter((k) => (anomalies[k] ?? 0) > 0);
  const chips = [
    ['warning', 'attention'],
    ['waiting', 'attention'],
    ['unknown', 'unknown'],
    ['optional', 'optional'],
  ].filter(([k]) => (anomalies[k] ?? 0) > 0);
  const barTotal = POD_KINDS.reduce((sum, k) => sum + (pods[k] ?? 0), 0);
  let x = 0;
  return (
    <div className={`ds-tile${empty ? ' ds-tile--empty' : ''}${noPermission ? ' ds-tile--no-permission' : ''}`} data-namespace={name}>
      {tabs.length > 0 && (
        <div className="ds-tile__tabs">
          {tabs.map((k) => (
            <span key={k} className={`ds-tile__tab ds-tile__tab--${k}`}>
              <Icon status="x" />
              <span>{anomalies[k]}</span>
              <span>{labels[k]}</span>
            </span>
          ))}
        </div>
      )}
      <div className="ds-tile__head">
        <span className="ds-tile__name">{name}</span>
      </div>
      {noPermission ? (
        <div className="ds-tile__note">
          <Icon status="lock" />
          <span>{noPermission}</span>
        </div>
      ) : empty ? (
        <div className="ds-tile__note">{noResourcesIn(name)}</div>
      ) : (
        <>
          <div className="ds-tile__counts">
            <span className="ds-tile__count">
              <span className="ds-tile__number">{workloads}</span> <span>{labels.workloads}</span>
            </span>
            {POD_KINDS.map((k) => (
              <span key={k} className={`ds-tile__count ds-tile__count--${k}`}>
                <span className="ds-tile__number">{pods[k] ?? 0}</span> <span>{labels[`pods.${k}`]}</span>
              </span>
            ))}
          </div>
          <svg className="ds-tile__bar" viewBox="0 0 100 6" width="100%" height="6" preserveAspectRatio="none" aria-hidden="true" focusable="false">
            {POD_KINDS.map((k) => {
              const w = barTotal > 0 ? ((pods[k] ?? 0) / barTotal) * 100 : 0;
              const rect = <rect key={k} className={`ds-tile__bar-${k}`} x={x} y="0" width={w} height="6" />;
              x += w;
              return rect;
            })}
          </svg>
          {chips.length > 0 && (
            <div className="ds-tile__chips">
              {chips.map(([k, tone]) => (
                <Chip key={k} tone={tone}>
                  {`${anomalies[k]} ${labels[k] ?? ''}`.trim()}
                </Chip>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}

// C-17: a small card of the cluster band (Node, StorageClass, PV).
export function ClusterBandItem({ kind, name, value }) {
  return (
    <div className={`ds-band-item ds-band-item--${kind.toLowerCase()}`}>
      <Icon kind={kind} />
      <span className="ds-band-item__kind">{kind}</span>
      <span className="ds-band-item__name">{name}</span>
      {value && <span className="ds-band-item__value">{value}</span>}
    </div>
  );
}
