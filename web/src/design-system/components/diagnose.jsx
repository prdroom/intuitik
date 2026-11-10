// C-30 to C-34: Diagnose. No component here runs anything against the cluster: the only
// actions are copy, open, send to the model the user chose, and attach (DS-4).
import { useState } from 'react';
import { STRINGS, sendingSummary } from '../strings.js';
import { Button, Chip, CodeBlock, Icon } from './basics.jsx';

// C-30: the one-line summary of what will be sent, always visible. Numbers as given.
export function SendSummary({ provider, resources, events, logLines, noSecretValues = true }) {
  return (
    <div className="ds-send-summary" role="status">
      <Icon status="info" />
      <span>{sendingSummary({ provider, resources, events, logLines, noSecretValues })}</span>
    </div>
  );
}

const CATEGORIES = [
  ['summaries', STRINGS.resourceSummaries],
  ['events', STRINGS.events],
  ['logs', STRINGS.logs],
];

// C-31: category check boxes and per-resource exclusion. Every change emits the new
// selection; the caller recounts and passes the numbers to C-30.
export function ReviewPanel({ initial = { summaries: true, events: true, logs: true }, resources = [], excludeLabel, onChange }) {
  const [selection, setSelection] = useState({ categories: initial, excluded: [] });
  const update = (next) => {
    setSelection(next);
    if (onChange) {
      onChange(next);
    }
  };
  return (
    <div className="ds-review">
      <div className="ds-review__categories">
        {CATEGORIES.map(([key, label]) => (
          <label key={key} className="ds-review__category">
            <input type="checkbox" checked={selection.categories[key]} data-category={key} onChange={(e) => update({ ...selection, categories: { ...selection.categories, [key]: e.target.checked } })} />
            {label}
          </label>
        ))}
      </div>
      <ul className="ds-review__resources">
        {resources
          .filter((r) => !selection.excluded.includes(r.id))
          .map((r) => (
            <li key={r.id} className="ds-review__resource" data-resource-id={r.id}>
              <span className="ds-review__resource-name">{r.label}</span>
              <Button variant="text-link" label={`${excludeLabel} ${r.label}`} onClick={() => update({ ...selection, excluded: [...selection.excluded, r.id] })}>
                {STRINGS.exclude}
              </Button>
            </li>
          ))}
      </ul>
    </div>
  );
}

// C-32: Likely cause, explanation, Evidence chips in the link-state colours, Fix as a code
// block with Copy under "Run this yourself", Docs, indicator chips and an Attach
// suggestion. There is no run button.
export function AnswerBlock({ likelyCause, explanation, evidence = [], fix, docs = [], indicators = [], attach, copiedText, failedText, onEvidence, onAttach, onCopy }) {
  return (
    <div className="ds-answer">
      <div className="ds-answer__cap">{STRINGS.likelyCause}</div>
      <p className="ds-answer__cause">{likelyCause}</p>
      <p className="ds-answer__text">{explanation}</p>
      <div className="ds-answer__cap">{STRINGS.evidence}</div>
      <div className="ds-answer__chips">
        {evidence.map((e) => (
          <button key={e.label} type="button" className={`ds-evidence ds-evidence--${e.state ?? 'connected'}`} onClick={() => onEvidence && onEvidence(e.label)}>
            {e.label}
          </button>
        ))}
      </div>
      <div className="ds-answer__cap">{STRINGS.fix}</div>
      <p className="ds-answer__note">{STRINGS.runThisYourself}</p>
      <CodeBlock text={fix} copiedText={copiedText} failedText={failedText} onCopy={onCopy} />
      {docs.length > 0 && (
        <>
          <div className="ds-answer__cap">{STRINGS.docs}</div>
          <ul className="ds-answer__docs">
            {docs.map((d) => (
              <li key={d.href}>
                <a href={d.href}>{d.label}</a>
              </li>
            ))}
          </ul>
        </>
      )}
      {indicators.length > 0 && (
        <div className="ds-answer__chips">
          {indicators.map((t) => (
            <Chip key={t}>{t}</Chip>
          ))}
        </div>
      )}
      {attach && (
        <div className="ds-answer__attach">
          <span>{attach}</span>
          <Button variant="secondary" onClick={onAttach}>
            {STRINGS.attach}
          </Button>
        </div>
      )}
    </div>
  );
}

// C-33: first use of an external provider: what is sent, the log warning, the log default
// and "Agree and send" / "Don't send".
export function ConsentCard({ scope, initialLogDefault = 'include', onLogDefaultChange, onAgree, onDecline }) {
  const [logDefault, setLogDefault] = useState(initialLogDefault);
  const choose = (value) => {
    setLogDefault(value);
    if (onLogDefaultChange) {
      onLogDefaultChange(value);
    }
  };
  return (
    <div className="ds-card-panel ds-consent">
      <p className="ds-consent__scope">{scope}</p>
      <p className="ds-consent__warning">
        <Icon status="tri" />
        {STRINGS.logsCanContainSecrets}
      </p>
      <div className="ds-consent__choice" role="radiogroup">
        <label>
          <input type="radio" name="ds-log-default" value="include" checked={logDefault === 'include'} onChange={() => choose('include')} />
          {STRINGS.includeLogsByDefault}
        </label>
        <label>
          <input type="radio" name="ds-log-default" value="exclude" checked={logDefault === 'exclude'} onChange={() => choose('exclude')} />
          {STRINGS.excludeLogsByDefault}
        </label>
      </div>
      <div className="ds-consent__actions">
        <Button variant="primary" onClick={onAgree}>
          {STRINGS.agreeAndSend}
        </Button>
        <Button variant="secondary" onClick={onDecline}>
          {STRINGS.dontSend}
        </Button>
      </div>
    </div>
  );
}

// C-34: an error card with Retry.
export function ErrorCard({ text, onRetry }) {
  return (
    <div className="ds-card-panel ds-error-card">
      <p className="ds-error-card__text">
        <Icon status="x" />
        {text}
      </p>
      <Button variant="secondary" onClick={onRetry}>
        {STRINGS.retry}
      </Button>
    </div>
  );
}

// C-34: the not-set-up card with Open Settings.
export function NotSetUpCard({ text, onOpenSettings }) {
  return (
    <div className="ds-card-panel ds-not-set-up">
      <p className="ds-not-set-up__text">
        <Icon status="info" />
        {text}
      </p>
      <Button variant="primary" onClick={onOpenSettings}>
        {STRINGS.openSettings}
      </Button>
    </div>
  );
}
