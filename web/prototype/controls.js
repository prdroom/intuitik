// The control registry (packet FA2-T8 3.8, 6.3 C3): every interactive element the prototype
// renders carries data-control=<key>, and HANDLERS maps each key to what the prototype runs.
// Every key is a row of e2e/controls.json: navigate (changes what is shown), copy (writes the
// clipboard) or local (changes page state only). No key changes the cluster, and no handler
// sends a network request.
import { SCREENS } from './states.js';

// writeClipboard is the one copy action (class copy).
export function writeClipboard(text) {
  if (typeof navigator === 'undefined' || !navigator.clipboard || !navigator.clipboard.writeText) {
    return Promise.reject(new Error('clipboard unavailable'));
  }
  return navigator.clipboard.writeText(text);
}

// HANDLERS[key](dispatch, argument) runs the control. Actions go to the reducer of states.js.
export const HANDLERS = {
  'context-selector': (d) => d(['context']),
  'find-field': (d, query) => d(['find', query ?? '']),
  'find-result': (d, hit) => d(['findPick', hit]),
  tile: (d, ns) => d(['tile', ns]),
  card: (d, sel) => d(sel.t === 'row' ? ['row', sel.id] : ['card', sel]),
  expand: (d, uid) => d(['expand', uid]),
  'zoom-in': (d) => d(['zoom', 1]),
  'zoom-out': (d) => d(['zoom', -1]),
  breadcrumb: (d, stage) => d(['stage', stage]),
  esc: (d) => d(['esc']),
  'needs-attention-row': (d, item) => d(['attention', item]),
  'inspector-tab': (d, tab) => d(['tab', tab]),
  'inspector-subtab': (d, sub) => d(['sub', sub]),
  'container-selector': (d, name) => d(['container', name]),
  follow: (d) => d(['follow']),
  'previous-container': (d) => d(['previous']),
  'show-previous-logs': (d) => d(['previous']),
  'open-namespace': (d, name) => d(['openNs', name]),
  'choose-another-context': (d) => d(['context']),
  'retry-connection': (d) => d(['retryConnection']),
  'retry-partial': (d) => d(['retryPartial']),
  'shortcut-list': (d) => d(['shortcuts']),
  'settings-link': (d) => d(['settings']),
  'copy-yaml': (d, text) => writeClipboard(text),
  'copy-setting-value': (d, text) => writeClipboard(text),
  'copy-fix': (d, text) => writeClipboard(text),
  'ask-why': (d, sel) => d(['askWhy', sel]),
  ask: (d) => d(['ask']),
  'question-field': () => {},
  send: (d) => d(['send']),
  'review-checkbox': (d, change) => d(['reviewCat', change]),
  'review-remove': (d, id) => d(['reviewRemove', id]),
  attach: (d) => d(['attach']),
  'agree-and-send': (d) => d(['agree']),
  'dont-send': (d) => d(['decline']),
  'retry-error-card': (d) => d(['retryError']),
  'provider-field': (d, provider) => d(['setting', { provider }]),
  'model-field': (d, model) => d(['setting', { model }]),
  'api-key-field': (d, typed) => d(['setting', { key: typed ? 'typed' : 'none' }]),
  'save-key-on-computer': (d) => d(['setting', { save: 'computer' }]),
  'use-for-session': (d) => d(['setting', { save: 'session' }]),
  'test-connection': (d) => d(['testConnection']),
  'delete-saved-key': (d) => d(['deleteKey']),
  'log-default': (d, value) => d(['setting', { logDefault: value }]),
};

// The prototype-only control strip (class navigate, keys strip-<screen id>).
for (const id of Object.keys(SCREENS)) {
  HANDLERS[`strip-${id}`] = (d) => d(['screen', id]);
}

// run is how components call a control.
export const run = (dispatch, key, arg) => HANDLERS[key](dispatch, arg);

// Elements that the design system renders itself get their key from this table (selector
// within the component's own markup -> key); a label never decides it. A copy block takes the
// key its wrapper names in data-copy-control.
export const DS_RULES = [
  ['.ds-context__button, .ds-context__option', 'context-selector'],
  ['.ds-topbar > button.ds-button:has(> kbd)', 'find-field'],
  ['.ds-topbar > button.ds-button--secondary:not(:has(> kbd))', 'settings-link'],
  ['.ds-topbar > button.ds-button--primary', 'ask'],
  ['.ds-breadcrumb button', 'breadcrumb'],
  ['.ds-attention-group__head, .ds-attention-item__select', 'needs-attention-row'],
  ['.ds-attention-item > button.ds-button', 'ask-why'],
  ['.ds-inspector > .ds-tabs:not(.ds-tabs--sub) > [role="tab"]', 'inspector-tab'],
  ['.ds-inspector > .ds-tabs--sub > [role="tab"]', 'inspector-subtab'],
  ['.ds-zoom > button.ds-button:first-child', 'zoom-in'],
  ['.ds-zoom > button.ds-button:last-child', 'zoom-out'],
  ['.ds-review input[type="checkbox"]', 'review-checkbox'],
  ['.ds-review__resource button.ds-button', 'review-remove'],
  ['.ds-consent input[type="radio"]', 'log-default'],
  ['.ds-consent button.ds-button--primary', 'agree-and-send'],
  ['.ds-consent button.ds-button--secondary', 'dont-send'],
  ['.ds-error-card button.ds-button', 'retry-error-card'],
  ['.ds-not-set-up button.ds-button', 'settings-link'],
  ['.ds-connection button.ds-button--primary', 'retry-connection'],
  ['.ds-connection button.ds-button--secondary', 'choose-another-context'],
  ['[data-copy-control] .ds-code button.ds-button', (el) => el.closest('[data-copy-control]').dataset.copyControl],
];

// tagControls gives every element a rule matches its key, unless it already has one.
export function tagControls(root) {
  for (const [selector, key] of DS_RULES) {
    for (const el of root.querySelectorAll(selector)) {
      if (!el.hasAttribute('data-control')) {
        el.setAttribute('data-control', typeof key === 'function' ? key(el) : key);
      }
    }
  }
}

// watchControls keeps tagging as the design system's components render new elements.
export function watchControls(root) {
  tagControls(root);
  const observer = new MutationObserver(() => tagControls(root));
  observer.observe(root, { childList: true, subtree: true });
  return observer;
}
