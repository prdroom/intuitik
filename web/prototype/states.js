// The prototype's state, the one reducer every control goes through (controls.js), and the
// screen states of packet FA2-T8 3.3. Each screen is the base state of its environment plus a
// list of the same actions the controls send, so a screen reached by clicking and the same
// screen reached through the strip are one state. `screenOf` names the screen a state is.

// The environment: what the prototype's fixed "server" and "provider" answer. Only the strip
// changes it (a server event or a different setup), never a product control.
export const BASE_ENV = {
  phase: 'ready', // ready | loading | failed
  session: null, // null | B-NS | B-RF | B-ST
  listed: true, // the namespace list is readable
  llm: 'ok', // ok | none | auth | rate | timeout | local-down
  consented: true, // the provider's first-use consent was given before
  keyFile: 'ok', // ok | locked
  paused: false,
  updated: '14:32:05',
  replaced: false, // the J-8 Pod replacement arrived
};

export const BASE_STATE = {
  env: BASE_ENV,
  frame: null,
  level: 'cluster',
  ns: null,
  workload: null,
  sel: null, // { t: 'node', id } | { t: 'row', id } | { t: 'ghost', id } | { t: 'unknown', id }
  tab: 'details',
  sub: 'summary',
  logs: { container: null, previous: false, follow: false },
  opened: [],
  diag: null, // { view, about, excluded, cats, withoutLogs, attached, error }
  settings: null, // { provider, model, key, save, test, del, logDefault }
  find: null, // { query, picked }
  shortcuts: false,
  zoom: 0,
};

const FRESH_DIAG = { view: 'compose', about: null, excluded: [], cats: { summaries: true, events: true, logs: true }, withoutLogs: false, attached: false, error: null };
const FRESH_SETTINGS = { provider: 'Anthropic', model: 'sample-model-large', key: 'saved', save: 'computer', test: null, del: null, logDefault: 'include' };
const LLM_ERROR = { auth: 'errAuth', rate: 'errRate', timeout: 'errTimeout', 'local-down': 'errLocal' };

function select(s, sel) {
  return { ...s, sel, tab: 'details', sub: 'summary', logs: BASE_STATE.logs, diag: null, settings: null, find: null };
}

function send(s) {
  const d = s.diag ?? FRESH_DIAG;
  if (s.env.llm === 'none') {
    return s;
  }
  if (!s.env.consented) {
    return { ...s, diag: { ...d, view: 'consent' } };
  }
  if (LLM_ERROR[s.env.llm]) {
    return { ...s, diag: { ...d, view: 'error', error: LLM_ERROR[s.env.llm] } };
  }
  return { ...s, diag: { ...d, view: 'answer', withoutLogs: !d.cats.logs } };
}

// reduce applies one action. Actions are [type, argument].
export function reduce(s, [type, arg]) {
  switch (type) {
    case 'env':
      return { ...s, env: { ...s.env, ...arg } };
    case 'frame':
      return { ...s, frame: arg };
    case 'tile':
      return { ...select(s, null), level: 'namespace', ns: arg, workload: null };
    case 'card':
      return select(s, { t: arg.t ?? 'node', id: arg.id });
    case 'row':
      return select(s, { t: 'row', id: arg });
    case 'expand':
      return { ...select(s, { t: 'node', id: arg }), level: 'workload', workload: arg };
    case 'stage':
      if (arg === 'cluster') {
        return { ...select(s, null), level: 'cluster', ns: null, workload: null };
      }
      if (arg === 'namespace' && s.ns) {
        return { ...s, level: 'namespace', workload: null };
      }
      if (arg === 'workload' && s.workload) {
        return { ...s, level: 'workload' };
      }
      return s;
    case 'esc':
      if (s.find || s.shortcuts || s.settings) {
        return { ...s, find: null, shortcuts: false, settings: null };
      }
      if (s.level === 'workload') {
        return { ...s, level: 'namespace', workload: null };
      }
      if (s.level === 'namespace') {
        return { ...select(s, null), level: 'cluster', ns: null };
      }
      return s;
    case 'attention':
      return { ...select(s, { t: arg.t, id: arg.id }), level: 'namespace', ns: arg.ns, workload: null };
    case 'tab':
      return { ...s, tab: arg, diag: arg === 'diagnose' ? (s.diag ?? FRESH_DIAG) : s.diag, settings: null };
    case 'sub':
      return { ...s, sub: arg, logs: BASE_STATE.logs };
    case 'container':
      return { ...s, logs: { ...s.logs, container: arg } };
    case 'follow':
      return { ...s, logs: { ...s.logs, follow: !s.logs.follow } };
    case 'previous':
      return { ...s, logs: { ...s.logs, previous: !s.logs.previous } };
    case 'openNs':
      return s.opened.includes(arg) ? s : { ...s, opened: [...s.opened, arg] };
    case 'askWhy':
      return { ...s, sel: arg ?? s.sel, tab: 'diagnose', diag: { ...FRESH_DIAG, about: arg ?? s.sel }, settings: null, find: null };
    case 'ask':
      return { ...s, tab: 'diagnose', diag: { ...FRESH_DIAG, about: s.sel }, settings: null, find: null };
    case 'reviewCat':
      return { ...s, diag: { ...s.diag, cats: { ...s.diag.cats, [arg.cat]: arg.on } } };
    case 'reviewRemove':
      return { ...s, diag: { ...s.diag, excluded: [...s.diag.excluded, arg] } };
    case 'send':
      return send(s);
    case 'agree':
      return send({ ...s, env: { ...s.env, consented: true } });
    case 'decline':
      return { ...s, diag: { ...s.diag, view: 'declined' } };
    case 'retryError':
      return send(s);
    case 'attach':
      return { ...s, diag: { ...s.diag, attached: true } };
    case 'settings':
      return { ...s, settings: { ...FRESH_SETTINGS, logDefault: s.settings?.logDefault ?? FRESH_SETTINGS.logDefault }, find: null };
    case 'setting':
      return { ...s, settings: { ...s.settings, ...arg } };
    case 'testConnection':
      return { ...s, settings: { ...s.settings, test: s.env.llm === 'auth' ? 'auth' : 'ok' } };
    case 'deleteKey':
      return { ...s, settings: { ...s.settings, del: s.env.keyFile === 'locked' ? 'failed' : 'ok', key: s.env.keyFile === 'locked' ? s.settings.key : 'none' } };
    case 'find':
      return { ...s, find: { query: arg, picked: false }, settings: null };
    case 'findPick':
      return { ...select(s, { t: 'node', id: arg.id }), level: 'namespace', ns: arg.ns, workload: null };
    case 'shortcuts':
      return { ...s, shortcuts: !s.shortcuts };
    case 'zoom':
      return { ...s, zoom: s.zoom + arg };
    case 'retryConnection':
    case 'retryPartial':
    case 'context':
      return s;
    default:
      throw new Error(`unknown action ${type}`);
  }
}

const SHOP_WEB = { t: 'node', id: 'u-shop-service-web' };
const ASK_WEB = [['tile', 'shop'], ['card', SHOP_WEB], ['askWhy', SHOP_WEB]];

// The screens of packet 3.3: [environment change, actions].
export const SCREENS = {
  P01: [{}, []],
  P02: [{}, [['tile', 'shop']]],
  P03: [{}, [['tile', 'shop'], ['expand', 'u-shop-deployment-cart']]],
  P04: [{}, [['tile', 'batch'], ['expand', 'u-batch-deployment-crash']]],
  P05: [{}, [['tile', 'media'], ['expand', 'u-media-deployment-transcoder']]],
  P06: [{}, [['tile', 'shop'], ['card', SHOP_WEB]]],
  P07: [{}, [['tile', 'shop'], ['card', { t: 'node', id: 'u-shop-service-api' }]]],
  P08: [{}, [['tile', 'payments'], ['card', { t: 'node', id: 'u-payments-deployment-payments-api' }]]],
  P09: [{}, [['tile', 'db'], ['card', { t: 'node', id: 'u-db-persistentvolumeclaim-data-cache' }]]],
  P10: [{}, [['tile', 'shop'], ['row', 'shop:r?cart-pods']]],
  P11: [{}, [['tile', 'db'], ['row', 'db:r?data-db-0-pv']]],
  P12: [{}, [['tile', 'payments'], ['row', 'payments:r?payments-config']]],
  P13: [{}, [['tile', 'db'], ['row', 'db:r?db-port']]],
  P14: [{}, [['tile', 'payments'], ['row', 'payments:r?payment-api']]],
  P15: [{}, [['tile', 'payments'], ['row', 'payments:r?feature-flags']]],
  P16: [{}, [['tile', 'shop'], ['card', { t: 'ghost', id: 'ghost:Pod:app=web' }]]],
  P17: [{}, [['tile', 'payments'], ['card', { t: 'ghost', id: 'ghost:ConfigMap:payments-config' }]]],
  P18: [{}, [['tile', 'db'], ['card', { t: 'ghost', id: 'ghost:StorageClass:fast' }]]],
  P19: [{}, [['tile', 'shop'], ['card', { t: 'ghost', id: 'ghost:Service:checkout' }]]],
  P20: [{}, [['tile', 'shop'], ['card', { t: 'node', id: 'u-shop-deployment-api' }]]],
  P21: [{}, [['tile', 'shop'], ['card', { t: 'node', id: 'u-shop-secret-db-credentials' }], ['sub', 'yaml']]],
  P22: [{}, [['tile', 'shop'], ['card', SHOP_WEB], ['sub', 'events']]],
  P23: [{}, [['tile', 'shop'], ['expand', 'u-shop-deployment-cart'], ['card', { t: 'node', id: 'u-shop-pod-cart-5b7d-k4' }], ['sub', 'logs']]],
  P24: [{}, [['tile', 'batch'], ['expand', 'u-batch-deployment-crash'], ['card', { t: 'node', id: 'u-batch-pod-crash-7d4b-p6' }], ['sub', 'logs']]],
  P25: [{}, [['tile', 'shop'], ['card', SHOP_WEB], ['row', 'shop:r?web-pods']]],
  P26: [{}, [['tile', 'payments'], ['card', { t: 'node', id: 'u-payments-deployment-payments-api' }], ['sub', 'events']]],
  P27: [{ llm: 'none' }, ASK_WEB],
  P28: [{}, [...ASK_WEB, ['reviewRemove', 'u-shop-ingress-shop']]],
  P29: [{ consented: false }, [['settings'], ['testConnection'], ['ask'], ['send']]],
  P30: [{ consented: false }, [['settings'], ['testConnection'], ['ask'], ['send'], ['decline']]],
  P31: [{}, [...ASK_WEB, ['reviewRemove', 'u-shop-ingress-shop'], ['send']]],
  P32: [{}, [...ASK_WEB, ['reviewRemove', 'u-shop-ingress-shop'], ['reviewCat', { cat: 'logs', on: false }], ['send']]],
  P33: [{ llm: 'auth' }, [...ASK_WEB, ['send']]],
  P34: [{ llm: 'rate' }, [...ASK_WEB, ['send']]],
  P35: [{ llm: 'timeout' }, [...ASK_WEB, ['send']]],
  P36: [{ llm: 'local-down' }, [...ASK_WEB, ['send']]],
  P37: [{ consented: false }, [['settings']]],
  P38: [{ consented: false }, [['settings'], ['testConnection']]],
  P39: [{ consented: false, llm: 'auth' }, [['settings'], ['testConnection']]],
  P40: [{ consented: false }, [['settings'], ['deleteKey']]],
  P41: [{ consented: false, keyFile: 'locked' }, [['settings'], ['deleteKey']]],
  P42: [{}, [['find', 'cart']]],
  P43: [{}, [['find', 'nothing-here']]],
  P44: [{}, [['find', 'api']]],
  P45: [{}, [['find', 'cart'], ['findPick', { ns: 'shop', id: 'u-shop-deployment-cart' }]]],
  P46: [{ phase: 'loading' }, []],
  P47: [{ phase: 'failed' }, []],
  P48: [{ listed: false }, []],
  P49: [{ listed: false }, [['openNs', 'payments']]],
  P50: [{}, [['tile', 'media']]],
  P51: [{}, [['tile', 'default']]],
  P53: [{ updated: '14:33:10' }, []],
  P54: [{ replaced: true, updated: '14:32:41' }, [['tile', 'shop'], ['expand', 'u-shop-deployment-cart']]],
  P55: [{}, [['tile', 'kube-system']]],
  P56: [{ session: 'B-NS' }, []],
  P57: [{ session: 'B-RF' }, []],
  P58: [{ session: 'B-ST' }, []],
  P59: [{}, [['tile', 'payments'], ['expand', 'u-payments-deployment-payments-api'], ['card', { t: 'node', id: 'u-payments-pod-payments-api-6d8f-t2' }]]],
  P60: [{}, [['frame', 1024]]],
  P61: [{}, [['frame', 720]]],
  P62: [{}, [['frame', 1024], ['tile', 'shop']]],
  P63: [{}, [['frame', 720], ['tile', 'shop']]],
  P64: [{}, [['frame', 1024], ['tile', 'shop'], ['card', { t: 'node', id: 'u-shop-deployment-api' }]]],
  P65: [{}, [['frame', 720], ['tile', 'shop'], ['card', { t: 'node', id: 'u-shop-deployment-api' }]]],
};

// Row ids depend on the order of the data; a screen names a row by a stable alias that
// `resolveRow` maps to the row id of the loaded data.
export const ROW_ALIASES = {
  'shop:r?cart-pods': { from: 'u-shop-service-cart', kind: 'service-pod' },
  'shop:r?web-pods': { from: 'u-shop-service-web', kind: 'service-pod' },
  'db:r?data-db-0-pv': { from: 'u-db-persistentvolumeclaim-data-db-0', kind: 'pvc-pv' },
  'db:r?db-port': { from: 'u-db-service-db', kind: 'service-pod' },
  'payments:r?payments-config': { from: 'u-payments-deployment-payments-api', kind: 'pod-configmap', state: 'broken' },
  'payments:r?feature-flags': { from: 'u-payments-deployment-payments-api', kind: 'pod-configmap', state: 'optional' },
  'payments:r?payment-api': { from: 'u-payments-deployment-payments-api', kind: 'pod-secret' },
};

// buildScreen returns the state of a screen id, with row aliases resolved by `resolveRow`.
export function buildScreen(id, resolveRow) {
  const [env, actions] = SCREENS[id];
  let s = { ...BASE_STATE, env: { ...BASE_ENV, ...env } };
  for (const [type, arg] of actions) {
    s = reduce(s, [type, type === 'row' ? resolveRow(arg) : arg]);
  }
  return s;
}

// key is the part of a state that decides what the screen shows. The opened namespaces and
// the list permission only change the Cluster stage.
export function key(s) {
  const k = { ...s, find: s.find ? { query: s.find.query } : null };
  if (s.level !== 'cluster') {
    k.opened = [];
    k.env = { ...s.env, listed: true };
  }
  return JSON.stringify(k);
}

// screenOf names the screen a state is, or '' when it is none of the 65.
export function screenOf(state, index) {
  return index.get(key(state)) ?? '';
}

export function screenIndex(resolveRow) {
  const index = new Map();
  for (const id of Object.keys(SCREENS)) {
    const k = key(buildScreen(id, resolveRow));
    if (index.has(k)) {
      throw new Error(`screens ${index.get(k)} and ${id} are the same state`);
    }
    index.set(k, id);
  }
  return index;
}
