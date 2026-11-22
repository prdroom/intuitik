// The UI strings of the prototype (packet FA2-T8 5 C2, UI.md 10 B): every text the prototype
// shows comes from this file, from the design system's fixed chrome (re-exported below), from
// the M1-T2 browser texts (re-exported, byte for byte) or from the fixed data as the server
// would send it (node and bridge texts, refusals). Non-ASCII characters are written as
// escapes: \u2014 em dash, \u00b7 middle dot, \u2026 ellipsis, \u2190 and \u2192 arrows.
import { STRINGS as DS, loadingCluster, noResourcesIn, updatesPaused } from '../src/design-system/strings.js';
import { NO_SESSION, REFUSED, STOPPED } from '../src/auth/texts.js';

export { DS };
export const SESSION_LINES = { 'B-NS': NO_SESSION, 'B-RF': REFUSED, 'B-ST': STOPPED };

// The bridge sentences of UI.md 4.4. `cause` is the server's text of the bridge (FA2-T7a 3.2);
// `sentence` is what every place of the screen shows for it (map card reason line, Inspector,
// Needs attention).
export const BRIDGE_SENTENCES = [
  { id: 'a3-selector', state: 'broken', cause: 'selector app=web matches 0 Pods', sentence: 'Broken \u2014 selector app=web matches 0 Pods' },
  { id: 'a4-named-port', state: 'broken', cause: 'targetPort "http": no container port with this name', sentence: 'Broken \u2014 targetPort "http": no container port with this name' },
  { id: 'a5-configmap', state: 'broken', cause: 'ConfigMap payments-config not found', sentence: 'Broken \u2014 ConfigMap payments-config not found' },
  { id: 'a6-storageclass', state: 'broken', cause: 'StorageClass "fast" not found (claim cannot bind)', sentence: 'Broken \u2014 StorageClass "fast" not found (claim cannot bind)' },
  { id: 'f18-route', state: 'broken', cause: 'Service checkout not found', sentence: 'Broken \u2014 Service checkout not found' },
  { id: 'waiting-claim', state: 'waiting', cause: 'data-db-0 waits for consumer Pod db-0', sentence: 'Waiting \u2014 data-db-0 waits for consumer Pod db-0' },
  { id: 'warning-port', state: 'warning', cause: 'targetPort 9000 is not declared by the container (traffic not verifiable)', sentence: 'Warning \u2014 targetPort 9000 is not declared by the container (traffic not verifiable)' },
  { id: 'unknown-secrets', state: 'unknown', cause: 'no permission to read Secrets', sentence: 'Unknown \u2014 no permission to read Secrets (the view role does not include Secrets)' },
  { id: 'optional-configmap', state: 'optional', cause: 'ConfigMap feature-flags not found', sentence: 'Optional \u2014 ConfigMap feature-flags not found' },
];

const STATE_WORD = { broken: 'Broken', waiting: 'Waiting', warning: 'Warning', unknown: 'Unknown', optional: 'Optional', connected: 'Connected' };

// bridgeSentence is the UI.md 4.4 sentence of a bridge state and its server text; a text the
// dictionary does not list is shown in the same form, "<State> \u2014 <text>".
export function bridgeSentence(state, text) {
  const hit = BRIDGE_SENTENCES.find((s) => s.state === state && s.cause === text);
  if (hit) {
    return hit.sentence;
  }
  return text ? `${STATE_WORD[state]} \u2014 ${text}` : STATE_WORD[state];
}

export const stateWord = (state) => STATE_WORD[state];

const SAMPLE_MODEL = 'sample-model-large';
const KEY_PATH = '~/.config/intuitik/config.yaml';

export const S = {
  // UI.md 4.4 rows that are not bridge sentences.
  unsupportedRoute: 'Unsupported version v1alpha1',
  ownerMissing: 'Owner Deployment/web not found',
  // UI.md 8 state screens.
  loading: loadingCluster(7, 12),
  noList: "You can't list namespaces in this cluster. Showing shop from your kubeconfig.",
  openNamespace: 'Open namespace',
  noPermission: 'No permission',
  emptyDefault: noResourcesIn('default'),
  paused: updatesPaused('14:32:05'),
  nothing: DS.nothingNeedsAttention,
  countingOnly: 'Counting only namespaces you can read',
  connectionFailure:
    'Cannot reach the API server https://127.0.0.1:6443 of context kind-intuitik-qa: dial tcp 127.0.0.1:6443: connect: connection refused. Check your VPN or network connection, then retry from the intuitik tab or restart intuitik.',
  // UI.md 7 error cards.
  errAuth: 'Authentication failed \u2014 check the API key in Settings',
  errRate: 'Rate limit reached \u2014 try again in 40 s',
  errTimeout: 'No response after 60 s \u2014 try again or use a smaller question',
  errLocal: "Can't reach http://localhost:11434 \u2014 is your model server running?",
  // UI.md 6 Details and 7 Diagnose and Settings.
  secretNotice: 'Values are never shown, copied or sent to the model.',
  hidden: '<hidden>',
  noLogs: 'No logs \u2014 the container has terminated',
  showPreviousLogs: 'Show previous container logs',
  previousContainer: 'Previous container',
  follow: 'Follow',
  container: 'Container',
  lastLines: 'Last 500 lines',
  noSampleLines: 'No log lines in the sample data',
  noPermissionEvents: 'No permission to read events in payments',
  selectSomething: 'Select a resource or a bridge',
  lookingFor: 'Looking for',
  waitingFor: 'Waiting for',
  copySelector: 'Copy selector',
  copySetting: 'Copy setting',
  copied: 'Copied',
  copyFailed: "Couldn't copy",
  settingsValues: 'Settings',
  bridges: 'Links',
  noBridges: 'No links',
  owner: 'Owner',
  namespace: 'Namespace',
  kind: 'Kind',
  state: 'State',
  healthy: 'Healthy',
  linkHead: (kind) => `Link \u00b7 ${kind}`,
  question: 'Question',
  questionFor: (kind, name) => `Why is ${kind} ${name} not working?`,
  questionServiceWeb: 'Why is Service web not reaching any Pod?',
  send: 'Send',
  sendTo: 'api.anthropic.com',
  sendToLocal: 'http://localhost:11434 (this computer)',
  review: 'Review',
  about: 'About',
  excludeLabel: 'Exclude',
  sendPreview: 'What will be sent',
  consentScope: 'Sending resource summaries, events and logs of the selected element to api.anthropic.com. Secret values are never sent.',
  useLocal: 'Use a local model instead',
  notConfigured: 'Diagnose is not set up. Choose a provider and add an API key in Settings.',
  conversationsNotSaved: 'Conversations are not saved. Closing this tab clears them.',
  withoutLogs: 'Diagnosed without logs',
  docsVersion: 'Docs for v1.31 (cluster is v1.32)',
  attachHint: 'To be sure, attach: previous logs of api-7f9c-q2',
  attached: 'Attached to the next question: previous logs of api-7f9c-q2',
  sampleAnswer: 'Sample answer (prototype text, not a model output)',
  settingsTitle: 'Settings',
  provider: 'Provider',
  providers: ['Anthropic', 'OpenAI', 'OpenAI-compatible endpoint'],
  model: 'Model',
  models: [SAMPLE_MODEL, 'sample-model-small'],
  apiKey: 'API key',
  keyMasked: '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022 (saved)',
  keySession: '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022 (this session only)',
  saveOnComputer: 'Save key on this computer',
  sessionOnly: 'Use for this session only',
  savePath: `Saved in ${KEY_PATH}`,
  testConnection: 'Test connection',
  connectedModel: `Connected \u00b7 ${SAMPLE_MODEL}`,
  deleteKey: 'Delete saved key',
  deleted: 'Deleted',
  couldntDelete: `Couldn't delete \u2014 remove the key from ${KEY_PATH} manually`,
  logDefault: 'Logs',
  dataOnComputer: 'Data on this computer',
  dataPaths: `Settings file: ${KEY_PATH} \u00b7 Documentation index cache: ~/.cache/intuitik/docs`,
  resetHint: 'To erase everything, run `intuitik reset` in a terminal',
  // Find (UI.md 11 P-1, packet 3.5).
  findLabel: 'Find a resource',
  noMatches: 'No matches',
  // The map.
  stage: { cluster: 'Cluster', namespace: 'Namespace', workload: 'Workload' },
  contextLabel: 'Context',
  zoomIn: 'Zoom in',
  zoomOut: 'Zoom out',
  shortcuts: '?',
  shortcutsLabel: 'Keyboard shortcuts',
  shortcutList: [
    '/  Find',
    'Tab  into the map, arrows between nodes',
    'Enter  select \u00b7 Shift+Enter  expand a workload',
    'Esc  one level up or close',
  ],
  updated: (time) => `Updated ${time}`,
  podsReady: (ready, total) => `Pods ${ready}/${total} ready`,
  restarts: (n) => `${n} restarts`,
  more: (n, kind) => `${n} more ${kind}`,
  ghostPods: (selector, count) => `Pods matching ${selector} \u00b7 ${count} found`,
  ghostNotFound: (kind, name) => `${kind} ${name} \u00b7 not found`,
  ghostLooking: (selector) => `Looking for label ${selector}`,
  ghostIn: (count, namespace) => `${count} found in ${namespace}`,
  // ghostTitle is what an empty place looks for (UI.md 4.2, 4.5).
  ghostTitle: (t) => (t.selector ? S.ghostPods(t.selector, 0) : t.claim ? `PersistentVolume for ${t.claim}` : S.ghostNotFound(t.kind, t.name)),
  replaced: (oldPod, newPod) => `${oldPod} replaced by ${newPod}`,
  tileLabels: {
    broken: 'broken',
    failing: 'failing',
    warning: 'warning',
    waiting: 'waiting',
    unknown: 'unknown',
    optional: 'optional',
    workloads: 'workloads',
    'pods.ok': 'running',
    'pods.progressing': 'pending',
    'pods.failing': 'failing',
  },
  bandNodes: (ready, total) => `Ready ${ready}/${total}`,
  bandDefault: '(default)',
  bandPv: (n) => `${n} volumes`,
  summaryCluster: (namespaces) => `${namespaces} namespaces`,
  summaryCount: (n, what) => `${n} ${what}`,
  optionalNote: 'Optional references are not counted here',
  readOnlyHint: 'Read-only',
  strip: 'Prototype control strip (not part of the product)',
  stripShow: (id) => id,
  // The comparison scenes (packet 6.4).
  sceneTitle: (renderer, scene) => `${renderer} \u00b7 ${scene}`,
};

// The sentence of a node's own state (FA2-T7b 3.1 texts as the server sends them).
export const nodeStateText = (nodeState) => (nodeState && nodeState.text) || '';
