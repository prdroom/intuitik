// Login and session of one tab (packet M1-T2 S3, S6, S8, S19).
//
// The opening address carries the one-time token in its fragment (#t=<T>). The tab
// exchanges it once in the body of POST /api/session and keeps the session token S
// only in its own sessionStorage. S leaves the tab only in request headers
// (Authorization, and the sess. WebSocket subprotocol); the CSRF value C stays in
// memory and is fetched again after a reload. Nothing is written to a cookie,
// localStorage, IndexedDB or a URL.
import { NO_SESSION, REFUSED, STOPPED } from './texts.js';

const SESSION_KEY = 'intuitik.session';

function Screen({ id, lines }) {
  return (
    <div id="intuitik-auth-screen" data-screen={id}>
      {lines.map((line) => (
        <p key={line}>{line}</p>
      ))}
    </div>
  );
}

const screens = {
  noSession: <Screen id="B-NS" lines={NO_SESSION} />,
  refused: <Screen id="B-RF" lines={REFUSED} />,
  stopped: <Screen id="B-ST" lines={STOPPED} />,
};

function readSession() {
  try {
    return window.sessionStorage.getItem(SESSION_KEY);
  } catch {
    return null;
  }
}

async function exchange(token) {
  const response = await fetch('/api/session', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ t: token }),
    cache: 'no-store',
    credentials: 'omit',
    redirect: 'error',
  });
  if (response.status === 403) {
    return { refused: true };
  }
  if (!response.ok) {
    return {};
  }
  const body = await response.json();
  window.sessionStorage.setItem(SESSION_KEY, body.session);
  return { session: body.session, csrf: body.csrf };
}

async function fetchCsrf(session) {
  const response = await fetch('/api/session', {
    headers: { Authorization: `Bearer ${session}` },
    cache: 'no-store',
    credentials: 'omit',
    redirect: 'error',
  });
  if (!response.ok) {
    return null;
  }
  return (await response.json()).csrf;
}

// connect opens the tab's WebSocket with the W1 subprotocols. Any close of the
// connection, with or without the stop notice, ends at the B-ST screen: the tab
// does not reconnect and does not reload (S8 (ii)).
function connect(session, csrf, onStop) {
  const socket = new WebSocket(`ws://${window.location.host}/api/ws`, [
    'intuitik.v1',
    `csrf.${csrf}`,
    `sess.${session}`,
  ]);
  let stopped = false;
  const stop = () => {
    if (!stopped) {
      stopped = true;
      onStop(screens.stopped);
    }
  };
  socket.addEventListener('message', (event) => {
    try {
      if (JSON.parse(event.data).type === 'stop') {
        stop();
      }
    } catch {
      // Not a notice: messages for the app.
    }
  });
  socket.addEventListener('close', stop);
  return socket;
}

// startAuth resolves to { session: true, ... } when the tab holds a session, or to
// { session: false, screen } with the screen to render (B-NS or B-RF). onStop
// receives the B-ST screen when the connection to the server ends.
export async function startAuth(onStop) {
  // Stub: no exchange and no connection yet.
  if (onStop) {
    return { session: false, screen: screens.noSession };
  }
  const hash = window.location.hash;
  let session = null;
  let csrf = null;
  if (hash.startsWith('#t=')) {
    // Remove the token from the address bar and the history entry before anything else.
    window.history.replaceState(null, '', window.location.pathname);
    let result;
    try {
      result = await exchange(hash.slice(3));
    } catch {
      result = {};
    }
    if (result.refused) {
      return { session: false, screen: screens.refused };
    }
    session = result.session ?? null;
    csrf = result.csrf ?? null;
  } else {
    session = readSession();
    if (session) {
      try {
        csrf = await fetchCsrf(session);
      } catch {
        csrf = null;
      }
    }
  }
  if (!session || !csrf) {
    return { session: false, screen: screens.noSession };
  }
  const socket = connect(session, csrf, onStop);
  return { session: true, socket };
}
