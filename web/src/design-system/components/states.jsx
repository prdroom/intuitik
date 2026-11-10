// C-40 to C-43: state screens.
import { STRINGS, loadingCluster } from '../strings.js';
import { Button, Icon, StatePill } from './basics.jsx';

// C-40: a connection state: the state pill, the caller's text (the M1-T3 K strings as
// given), "Retry" and "Choose another context".
export function ConnectionStateCard({ state, text, onRetry, onChooseContext }) {
  return (
    <div className="ds-card-panel ds-connection">
      <StatePill kind="link" state={state} />
      <p className="ds-connection__text">{text}</p>
      <div className="ds-connection__actions">
        <Button variant="primary" onClick={onRetry}>
          {STRINGS.retry}
        </Button>
        <Button variant="secondary" onClick={onChooseContext}>
          {STRINGS.chooseAnotherContext}
        </Button>
      </div>
    </div>
  );
}

// C-41: a band over the map: permission (grey) or paused (attention colour).
export function Banner({ variant = 'permission', text, children }) {
  return (
    <div className={`ds-banner ds-banner--${variant}`} role="status">
      <Icon status={variant === 'paused' ? 'clock' : 'lock'} />
      <span className="ds-banner__text">{text}</span>
      {children}
    </div>
  );
}

// C-42: "Loading cluster \u00b7 n of m resource types" and a bar.
export function LoadingProgress({ received, total }) {
  const w = total > 0 ? (received / total) * 100 : 0;
  return (
    <div className="ds-loading" role="status">
      <span className="ds-loading__text">{loadingCluster(received, total)}</span>
      <svg className="ds-loading__bar" viewBox="0 0 100 4" width="100%" height="4" preserveAspectRatio="none" aria-hidden="true" focusable="false">
        <rect className="ds-loading__track" x="0" y="0" width="100" height="4" />
        <rect className="ds-loading__done" x="0" y="0" width={w} height="4" />
      </svg>
    </div>
  );
}

// C-43: a dashed box with the caller's text.
export function EmptyState({ text }) {
  return (
    <div className="ds-empty">
      <span className="ds-empty__text">{text}</span>
    </div>
  );
}
