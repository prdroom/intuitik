// Fixed UI chrome named in the design system specification section 3 (and the UI.md
// sections that its rows cite), copied byte for byte. Components render these and the
// strings their caller passes; they make up no other text (specification DS-5).
// Non-ASCII characters are written as escapes: \u2014 em dash, \u00b7 middle dot,
// \u2026 ellipsis, \u2212 minus sign, \u2715 multiplication x.

export const STRINGS = {
  nothingNeedsAttention: 'Nothing needs attention',
  runThisYourself: 'Run this yourself \u2014 intuitik never changes your cluster.',
  agreeAndSend: 'Agree and send',
  dontSend: "Don't send",
  retry: 'Retry',
  chooseAnotherContext: 'Choose another context',
  openSettings: 'Open Settings',
  logsCanContainSecrets: 'Logs can contain secrets your app printed',
  includeLogsByDefault: 'Include logs by default',
  excludeLogsByDefault: 'Exclude logs by default',
  readOnly: 'Read-only',
  find: 'Find',
  findShortcut: '/',
  settings: 'Settings',
  ask: 'Ask',
  askWhy: 'Ask why',
  copy: 'Copy',
  likelyCause: 'Likely cause',
  evidence: 'Evidence',
  fix: 'Fix',
  docs: 'Docs',
  attach: 'Attach',
  details: 'Details',
  diagnose: 'Diagnose',
  summary: 'Summary',
  yaml: 'YAML',
  events: 'Events',
  logs: 'Logs',
  resourceSummaries: 'Resource summaries',
  zoomIn: '+',
  zoomOut: '\u2212',
  exclude: '\u2715',
  brokenMark: '\u2715',
};

export const STAGE_NAMES = {
  cluster: 'Cluster',
  namespace: 'Namespace',
  workload: 'Workload',
};

export const LINK_STATE_NAMES = {
  connected: 'Connected',
  broken: 'Broken',
  waiting: 'Waiting',
  warning: 'Warning',
  unknown: 'Unknown',
  optional: 'Optional',
};

export const NODE_STATE_NAMES = {
  healthy: 'Healthy',
  progressing: 'Progressing',
  failing: 'Failing',
  unknown: 'Unknown',
};

// C-22 group titles, in the order of UI.md section 5.
export const ATTENTION_GROUP_NAMES = {
  broken: 'Broken',
  failing: 'Failing',
  warning: 'Warning',
  unknown: 'Unknown',
  waiting: 'Waiting',
};

// C-16 empty Namespace (UI.md section 8).
export const noResourcesIn = (namespace) => `No resources in ${namespace}`;

// C-42 (UI.md section 8).
export const loadingCluster = (received, total) => `Loading cluster \u00b7 ${received} of ${total} resource types`;

// C-20 paused band (UI.md section 8).
export const updatesPaused = (time) => `Updates paused \u2014 showing data from ${time}. Reconnecting\u2026`;

// C-14 fold row (UI.md section 4.5).
export const foldedConnected = (count) => `+${count} connected`;

// C-10 broken-link suffix and mark text (UI.md section 4.3).
export const brokenLinks = (count) => `${count} broken link`;
export const withBrokenLinks = (text, count) => `${text} \u00b7 ${brokenLinks(count)}`;

// C-13 accessible name (UI.md section 9).
export const linkAccessibleName = (kind, from, to, state, text) => `${kind} bridge, ${from} to ${to}, ${state}, ${text}`;

// C-30 one-line summary (UI.md section 7).
export const sendingSummary = ({ provider, resources, events, logLines, noSecretValues }) => {
  const parts = [`${resources} resources`, `${events} events`, `${logLines} log lines`];
  if (noSecretValues) {
    parts.push('no Secret values');
  }
  return `Sending to ${provider}: ${parts.join(' \u00b7 ')}`;
};
