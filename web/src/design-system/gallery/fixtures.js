// The gallery's fixed mock data: one item per state or variant of every component of the
// design system specification section 3. No component imports this module. Reason lines
// are the full reason sentences of UI.md 4.4 (the longest case); cards cut them with
// cardReason, the Inspector and Needs attention items show them whole.

// UI.md 4.4, byte for byte (\u2014 is the em dash).
export const REASONS = {
  selector: 'Broken \u2014 selector app=web matches 0 Pods',
  namedPort: 'Broken \u2014 targetPort "http": no container port with this name',
  configMap: 'Broken \u2014 ConfigMap payments-config not found',
  storageClass: 'Broken \u2014 StorageClass "fast" not found (claim cannot bind)',
  httpRoute: 'Broken \u2014 Service checkout not found',
  waitingPvc: 'Waiting \u2014 data-db-0 waits for consumer Pod db-0',
  numericPort: 'Warning \u2014 targetPort 9000 is not declared by the container (traffic not verifiable)',
  noPermission: 'Unknown \u2014 no permission to read Secrets (the view role does not include Secrets)',
  optional: 'Optional \u2014 ConfigMap feature-flags not found',
};

// A 120-character reason (specification section 5, packet T-7 and B-2).
export const LONG_REASON = 'selector app=web,tier=frontend,track=stable matches 0 Pods in shop: every Pod of Deployment web carries track=canary now';

const REASON_BY_STATE = {
  broken: REASONS.selector,
  waiting: REASONS.waitingPvc,
  warning: REASONS.numericPort,
  unknown: REASONS.noPermission,
  optional: REASONS.optional,
};

const ROW_BY_STATE = {
  connected: { name: 'port', value: '80 \u2192 8080', fact: '3 Pods' },
  broken: { name: 'selects', value: 'app=web', fact: '0 Pods' },
  waiting: { name: 'volume', value: 'data-db-0' },
  warning: { name: 'port', value: '9000' },
  unknown: { name: 'envFrom', value: 'secret/db-credentials' },
  optional: { name: 'envFrom', value: 'configmap/feature-flags' },
};

export const LINK_STATE_IDS = ['connected', 'broken', 'waiting', 'warning', 'unknown', 'optional'];

const STATUS_ICONS = ['x', 'clock', 'tri', 'lock', 'info', 'ok', 'ring'];
const KIND_ICONS = ['Ingress', 'Service', 'Deployment', 'StatefulSet', 'DaemonSet', 'CronJob', 'Job', 'Pod', 'ConfigMap', 'Secret', 'PVC', 'PV', 'StorageClass', 'HPA', 'HTTPRoute'];

const item = (id, title, props = {}) => ({ id, component: id.split('.')[0], title, props });

const connectedRows = (n) =>
  Array.from({ length: n }, (_, i) => ({
    id: `ok-${i + 1}`,
    state: 'connected',
    name: ['selects', 'port', 'env', 'volume', 'class'][i % 5],
    value: ['app=web', '80 \u2192 8080', 'LOG_LEVEL', 'cache', 'standard'][i % 5],
  }));

export const GALLERY_ITEMS = [
  // C-01 Icon
  ...STATUS_ICONS.map((s) => item(`C-01.status.${s}`, `Status glyph ${s}`, { status: s })),
  ...KIND_ICONS.map((k) => item(`C-01.kind.${k.toLowerCase()}`, `Kind ${k}`, { kind: k })),
  item('C-01.kind.unknown', 'Unknown kind name: generic box', { kind: 'FlowSchema' }),

  // C-02 Chip
  ...['neutral', 'broken', 'attention', 'optional', 'unknown'].flatMap((tone) =>
    ['mono', 'sans'].map((font) => item(`C-02.${tone}.${font}`, `Chip ${tone}, ${font}`, { tone, font, text: font === 'mono' ? '3/3 ready' : 'ClusterIP' })),
  ),

  // C-03 StatePill
  ...LINK_STATE_IDS.map((s) => item(`C-03.link.${s}`, `Link state ${s}${s === 'connected' ? ' (renders nothing)' : ''}`, { kind: 'link', state: s, text: s === 'connected' ? 'selector app=web' : 'selector app=web' })),
  ...['healthy', 'progressing', 'failing', 'unknown'].map((s) =>
    item(`C-03.node.${s}`, `Node state ${s}${s === 'healthy' ? ' (renders nothing)' : ''}`, {
      kind: 'node',
      state: s,
      text: { healthy: 'Running', progressing: 'Pending \u00b7 scheduling', failing: 'CrashLoopBackOff \u00b7 7 restarts', unknown: 'No permission' }[s],
    }),
  ),

  // C-04 Button
  ...['primary', 'secondary', 'text-link'].flatMap((v) => [
    item(`C-04.${v}`, `Button ${v}`, { variant: v, text: { primary: 'Open Settings', secondary: 'Copy selector', 'text-link': 'Show previous container logs' }[v] }),
    item(`C-04.${v}.disabled`, `Button ${v}, disabled`, { variant: v, disabled: true, text: { primary: 'Open Settings', secondary: 'Copy selector', 'text-link': 'Show previous container logs' }[v] }),
  ]),

  // C-05 CodeBlock + Copy
  item('C-05.default', 'Code block with Copy (leading and trailing spaces kept)', {
    text: '  kubectl get pods -n shop -l app=web  ',
    copiedText: 'Copied',
    failedText: "Couldn't copy \u2014 select the text and copy it yourself",
  }),

  // C-10 StatusTab
  item('C-10.failing', 'Failing tab (solid border)', { state: 'failing', text: 'CrashLoopBackOff \u00b7 7 restarts' }),
  item('C-10.progressing', 'Progressing tab (dashed border)', { state: 'progressing', text: 'Pending \u00b7 scheduling' }),
  item('C-10.unknown', 'Unknown tab (grey)', { state: 'unknown', text: 'No permission' }),
  item('C-10.attention', 'Attention mark: broken-link propagation, no tab border or fill', { state: 'attention', text: '1 broken link' }),
  item('C-10.failing.broken-link', 'Failing tab with a broken link', { state: 'failing', text: 'CrashLoopBackOff \u00b7 7 restarts', brokenLinks: 1 }),
  item('C-10.healthy', 'Healthy: no tab', { state: 'healthy' }),

  // C-11 RefRow
  ...LINK_STATE_IDS.flatMap((s) => {
    const base = { id: `row-${s}`, state: s, ...ROW_BY_STATE[s] };
    const one = item(`C-11.${s}.one-line`, `${s}, one line`, { row: base });
    if (s === 'connected') {
      return [one];
    }
    return [one, item(`C-11.${s}.two-line`, `${s}, two lines (UI.md 4.4 reason)`, { row: { ...base, reason: REASON_BY_STATE[s] } })];
  }),
  item('C-11.broken.long-reason', 'Broken, 120-character reason (wraps, no ellipsis)', { row: { id: 'row-long', state: 'broken', name: 'selects', value: 'app=web', fact: '0 Pods', reason: LONG_REASON } }),
  item('C-11.connected.long-value', 'Value cut with an ellipsis, full value in title', {
    row: { id: 'row-value', state: 'connected', name: 'selects', value: 'app.kubernetes.io/name=web,app.kubernetes.io/instance=web-prod,tier=frontend' },
  }),

  // C-12 Port
  ...['in', 'out'].flatMap((d) => LINK_STATE_IDS.map((s) => item(`C-12.${d}.${s}`, `${d} port, ${s}`, { direction: d, state: s }))),

  // C-13 Link
  ...LINK_STATE_IDS.map((s) =>
    item(`C-13.${s}`, `Link ${s}`, { state: s, kind: 'Traffic', fromLabel: 'Service web', toLabel: 'Pods app=web', text: s === 'connected' ? 'selector app=web' : REASON_BY_STATE[s] }),
  ),

  // C-14 NodeCard
  item('C-14.compact.default', 'Compact card', {
    kind: 'Service',
    name: 'web',
    rows: [
      { id: 'sel', state: 'broken', name: 'selects', value: 'app=web', fact: '0 Pods', reason: REASONS.selector },
      { id: 'port', state: 'connected', name: 'port', value: '80 \u2192 8080' },
    ],
    chips: [{ text: 'ClusterIP' }],
    status: { state: 'attention', text: '1 broken link' },
  }),
  item('C-14.compact.folded', 'Compact card: 5 normal rows fold to 2 + "+3 connected", anomalous rows stay', {
    kind: 'Deployment',
    name: 'payments',
    rows: [
      ...connectedRows(3),
      { id: 'cfg', state: 'broken', name: 'envFrom', value: 'configmap/payments-config', reason: REASONS.configMap },
      ...connectedRows(5).slice(3),
      { id: 'flags', state: 'optional', name: 'envFrom', value: 'configmap/feature-flags', reason: REASONS.optional },
    ],
    chips: [{ text: '0/3 ready', tone: 'broken', font: 'mono' }, { text: '7 restarts', font: 'mono' }],
    status: { state: 'failing', text: 'CreateContainerConfigError', brokenLinks: 1 },
    owned: {
      kind: 'ReplicaSet',
      name: 'payments-6d4f9',
      items: [
        { id: 'p1', kind: 'Pod', name: 'payments-6d4f9-x2k1', state: 'failing', note: 'CreateContainerConfigError' },
        { id: 'p2', kind: 'Pod', name: 'payments-6d4f9-q8z3', state: 'failing', note: 'CreateContainerConfigError' },
      ],
    },
  }),
  item('C-14.compact.long-reason', 'Compact card with the 120-character reason', {
    kind: 'Service',
    name: 'web',
    rows: [
      { id: 'sel', state: 'broken', name: 'selects', value: 'app=web', fact: '0 Pods', reason: LONG_REASON },
      { id: 'port', state: 'warning', name: 'port', value: '9000', reason: REASONS.numericPort },
      { id: 'ok', state: 'connected', name: 'port', value: '443 \u2192 8443' },
    ],
    chips: [{ text: 'ClusterIP' }],
    status: { state: 'attention', text: '1 broken link' },
  }),
  item('C-14.expanded.default', 'Expanded card with its owned objects (ReplicaSet \u2283 Pod rows)', {
    mode: 'expanded',
    kind: 'StatefulSet',
    name: 'db',
    rows: [
      { id: 'vol', state: 'waiting', name: 'volume', value: 'data-db-0', reason: REASONS.waitingPvc },
      { id: 'sec', state: 'unknown', name: 'envFrom', value: 'secret/db-credentials', reason: REASONS.noPermission },
      { id: 'svc', state: 'connected', name: 'serviceName', value: 'db-headless' },
    ],
    chips: [{ text: '0/1 ready', tone: 'attention', font: 'mono' }],
    status: { state: 'progressing', text: 'Pending \u00b7 scheduling' },
    owned: { kind: 'Pod', name: 'db-0', items: [{ id: 'db-0', kind: 'Pod', name: 'db-0', state: 'progressing' }] },
  }),
  item('C-14.selected', 'Selected card', {
    kind: 'Ingress',
    name: 'shop',
    selected: true,
    rows: [{ id: 'route', state: 'connected', name: '/', value: 'web:80' }],
    chips: [{ text: 'nginx' }],
  }),
  item('C-14.unknown', 'Unknown card (name dimmed)', {
    kind: 'Secret',
    name: 'db-credentials',
    rows: [],
    chips: [],
    status: { state: 'unknown', text: 'No permission' },
  }),
  item('C-14.failing', 'Failing card', {
    kind: 'Pod',
    name: 'api-7f9c-q2',
    rows: [{ id: 'cm', state: 'connected', name: 'envFrom', value: 'configmap/api' }],
    chips: [{ text: '7 restarts', tone: 'broken', font: 'mono' }],
    status: { state: 'failing', text: 'CrashLoopBackOff \u00b7 7 restarts' },
  }),
  item('C-14.healthy', 'Healthy card: no tab, no coloured row', {
    kind: 'ConfigMap',
    name: 'api',
    rows: [],
    chips: [{ text: '4 keys', font: 'mono' }],
  }),

  // C-15 GhostCard
  item('C-15.broken', 'Ghost of a missing target', { variant: 'broken', kind: 'Pod', target: 'Pods matching app=web', result: '0 found in shop', description: 'Looking for label app=web' }),
  item('C-15.optional', 'Ghost of a missing optional target', { variant: 'optional', kind: 'ConfigMap', target: 'ConfigMap feature-flags', result: 'not found', description: 'optional \u00b7 not found' }),

  // C-16 NamespaceTile
  item('C-16.anomalies', 'Namespace with anomalies', {
    name: 'shop',
    workloads: 7,
    pods: { ok: 9, progressing: 1, failing: 3 },
    anomalies: { broken: 2, failing: 3, warning: 1, waiting: 1, unknown: 1, optional: 1 },
  }),
  item('C-16.healthy', 'Namespace without anomalies', { name: 'kube-system', workloads: 12, pods: { ok: 18, progressing: 0, failing: 0 }, anomalies: {} }),
  item('C-16.empty', 'Empty Namespace', { name: 'default', workloads: 0, pods: { ok: 0, progressing: 0, failing: 0 }, anomalies: {} }),
  item('C-16.no-permission', 'Namespace without permission', { name: 'billing', noPermission: 'No permission' }),

  // C-17 ClusterBand item
  item('C-17.node', 'Node', { kind: 'Node', name: 'kind-control-plane', value: 'Ready' }),
  item('C-17.storageclass', 'StorageClass', { kind: 'StorageClass', name: 'standard', value: 'default' }),
  item('C-17.pv', 'PV', { kind: 'PV', name: 'pvc-3f2a', value: '1Gi \u00b7 RWO' }),

  // C-20 TopBar
  item('C-20.normal', 'Top bar', { contexts: ['kind-dev', 'staging'], context: 'kind-dev', updated: 'Updated 14:32:05' }),
  item('C-20.paused', 'Top bar, updates paused', { contexts: ['kind-dev', 'staging'], context: 'kind-dev', pausedSince: '14:32:05' }),

  // C-21 Breadcrumb + StageSwitch
  ...['cluster', 'namespace', 'workload'].map((stage) =>
    item(`C-21.${stage}`, `Path and stage switch at ${stage}`, {
      stage,
      path: [
        { stage: 'cluster', label: 'kind-dev' },
        { stage: 'namespace', label: 'shop' },
        { stage: 'workload', label: 'Deployment web' },
      ].slice(0, ['cluster', 'namespace', 'workload'].indexOf(stage) + 1),
    }),
  ),

  // C-22 AttentionGroup + AttentionItem
  item('C-22.groups', 'Groups in order; Unknown and Waiting start collapsed; select and open or close', {
    groups: {
      broken: [
        { id: 'a1', location: 'shop \u203a Service web', value: 'selects app=web', reason: REASONS.selector },
        { id: 'a2', location: 'shop \u203a Deployment payments', value: 'envFrom configmap/payments-config', reason: REASONS.configMap },
      ],
      failing: [{ id: 'a3', location: 'shop \u203a Pod api-7f9c-q2', value: 'container api', reason: 'CrashLoopBackOff \u00b7 7 restarts' }],
      warning: [{ id: 'a4', location: 'shop \u203a Service metrics', value: 'port 9000', reason: REASONS.numericPort }],
      unknown: [{ id: 'a5', location: 'shop \u203a Deployment db', value: 'envFrom secret/db-credentials', reason: REASONS.noPermission }],
      waiting: [{ id: 'a6', location: 'shop \u203a PVC data-db-0', value: 'volume data-db-0', reason: REASONS.waitingPvc }],
    },
    footnote: 'Optional references are not counted here (F-5)',
  }),
  item('C-22.selected', 'Selected item', {
    selectedId: 'b1',
    groups: { broken: [{ id: 'b1', location: 'shop \u203a HTTPRoute shop', value: 'backendRef checkout', reason: REASONS.httpRoute }] },
  }),
  item('C-22.empty', 'Nothing needs attention', { groups: {}, updated: '14:32:05' }),

  // C-23 InspectorShell
  item('C-23.details.pod', 'Details of a Pod (Logs shown)', { kind: 'Pod', name: 'api-7f9c-q2', state: { state: 'failing', text: 'CrashLoopBackOff' } }),
  item('C-23.details.service', 'Details of a Service (no Logs)', { kind: 'Service', name: 'web' }),
  item('C-23.diagnose', 'Diagnose tab', { kind: 'Deployment', name: 'payments', initialTab: 'diagnose' }),

  // C-24 LinkEnds
  item('C-24.target', 'Link ends with a target', { from: { kind: 'Service', name: 'web' }, link: { state: 'warning', value: 'targetPort 9000' }, to: { kind: 'Pod', name: 'web-5d8c-k2' } }),
  item('C-24.ghost', 'Link ends with a missing target', { from: { kind: 'Service', name: 'web' }, link: { state: 'broken', value: 'selector app=web' }, ghost: { target: 'Pods matching app=web', result: '0 found in shop' } }),

  // C-25 DefinitionList
  item('C-25.default', 'Keys and values', {
    items: [
      { key: 'Namespace', value: 'shop' },
      { key: 'selector', value: 'app=web' },
      { key: 'ports', value: '80/TCP \u2192 8080' },
    ],
  }),

  // C-26 ZoomControl
  item('C-26.default', 'Zoom', { zoomInLabel: 'Zoom in', zoomOutLabel: 'Zoom out' }),

  // C-30 SendSummary
  item('C-30.external', 'Send summary, external provider', { provider: 'api.anthropic.com', resources: 4, events: 12, logLines: 200 }),
  item('C-30.local', 'Send summary, local model', { provider: 'http://localhost:11434 (this computer)', resources: 2, events: 0, logLines: 0 }),

  // C-31 ReviewPanel
  item('C-31.default', 'Review: categories and per-resource exclusion', {
    excludeLabel: 'Exclude',
    resources: [
      { id: 'r1', label: 'Service web' },
      { id: 'r2', label: 'Deployment web' },
      { id: 'r3', label: 'Pod web-5d8c-k2' },
    ],
  }),

  // C-32 AnswerBlock
  item('C-32.default', 'Answer', {
    likelyCause: 'The Service selects app=web, but the Pods are labelled app=web-v2.',
    explanation: 'The Deployment was relabelled in the last rollout. A NetworkPolicy is not involved: the Service has no endpoints at all.',
    evidence: [
      { label: 'selects app=web', state: 'broken' },
      { label: 'Deployment web', state: 'connected' },
    ],
    fix: 'kubectl -n shop patch service web -p \'{"spec":{"selector":{"app":"web-v2"}}}\'',
    docs: [{ label: 'Service selectors (v1.31)', href: '#C-32.default' }],
    indicators: ['Diagnosed without logs', 'Docs for v1.31 (cluster is v1.32)'],
    attach: 'To be sure, attach: previous logs of api-7f9c-q2',
    copiedText: 'Copied',
    failedText: "Couldn't copy \u2014 select the text and copy it yourself",
  }),

  // C-33 ConsentCard
  item('C-33.default', 'First use of an external provider', { scope: 'Sending to api.anthropic.com: resource summaries, events and logs of the selected element and its neighbours.' }),

  // C-34 ErrorCard / NotSetUpCard
  item('C-34.error', 'Error card', { text: 'Rate limit reached \u2014 try again in 40 s' }),
  item('C-34.not-set-up', 'Not set up', { text: 'Choose a model provider in Settings to ask why.' }),

  // C-40 ConnectionStateCard
  item('C-40.unknown', 'Connection state', { state: 'unknown', text: 'The cluster did not answer within 10 s.' }),
  item('C-40.broken', 'Connection state, refused', { state: 'broken', text: 'The cluster refused the credentials of this context.' }),

  // C-41 Banner
  item('C-41.permission', 'Permission band', { variant: 'permission', text: "You can't list namespaces in this cluster. Showing shop from your kubeconfig." }),
  item('C-41.paused', 'Paused band', { variant: 'paused', text: 'Updates paused \u2014 showing data from 14:32:05. Reconnecting\u2026' }),

  // C-42 LoadingProgress
  item('C-42.partial', 'Loading, 7 of 12', { received: 7, total: 12 }),
  item('C-42.start', 'Loading, 0 of 12', { received: 0, total: 12 }),

  // C-43 EmptyState
  item('C-43.default', 'Empty state', { text: 'No resources in default' }),
];

// Label texts the gallery passes to the Namespace tiles (callers own these strings).
export const TILE_LABELS = {
  workloads: 'workloads',
  'pods.ok': 'running',
  'pods.progressing': 'pending',
  'pods.failing': 'failing',
  broken: 'broken',
  failing: 'failing',
  warning: 'warning',
  waiting: 'waiting',
  unknown: 'unknown',
  optional: 'optional',
};

// Gallery chrome.
export const GALLERY_TEXT = {
  title: 'intuitik design system',
  intro: 'Every component of the design system specification, in every state and variant, with fixed mock data. Item ids are the units of review.',
  tokens: 'Tokens',
  changed: 'Changed from the canvas (text contrast)',
  canvas: 'Canvas',
  system: 'System',
  contrastTitle: 'Text contrast (WCAG 2.x, at least 4.5)',
  fonts: 'Fonts',
  sansSample: 'Geist \u2014 The quick brown fox jumps over the lazy dog 0123456789',
  monoSample: 'Geist Mono \u2014 selector app=web matches 0 Pods',
  events: 'Events',
  portCentres: 'Port centres',
  expand: 'Expand',
  collapse: 'Collapse',
};
