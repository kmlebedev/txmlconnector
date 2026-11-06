// Loopback fake of the terminal gateway v1 for connector tests.

import http from 'node:http';

/**
 * Start a fake gateway on an ephemeral loopback port.
 * routes: path -> JSON body object, or (req, url) => ({ status, body, hang }).
 * `calls` collects request paths with query strings, in order.
 * @returns {Promise<{url: string, close: () => Promise<void>, calls: string[],
 *   routes: object, token: string|undefined, setToken: (t?: string) => void}>}
 */
export async function startFakeGateway({ token, routes = {} } = {}) {
  const state = { token, routes, calls: [] };
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://127.0.0.1');
    state.calls.push(url.pathname + url.search);
    const send = (status, body) => {
      const data = JSON.stringify(body);
      res.writeHead(status, { 'content-type': 'application/json', 'content-length': Buffer.byteLength(data) });
      res.end(data);
    };
    if (state.token && req.headers['x-terminal-token'] !== state.token) {
      send(401, { error: { code: 'unauthorized', message: 'missing or invalid token' } });
      return;
    }
    const route = state.routes[url.pathname];
    if (route === undefined) {
      send(404, { error: { code: 'not_found', message: `no route ${url.pathname}` } });
      return;
    }
    const out = typeof route === 'function' ? route(req, url) : { status: 200, body: route };
    if (out.hang) return; // never answer: exercises client timeouts
    send(out.status ?? 200, out.body);
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const { port } = server.address();
  return {
    url: `http://127.0.0.1:${port}`,
    calls: state.calls,
    get routes() { return state.routes; },
    set routes(r) { state.routes = r; },
    get token() { return state.token; },
    setToken(t) { state.token = t; },
    close() {
      return new Promise((resolve) => {
        server.closeAllConnections?.();
        server.close(() => resolve());
      });
    },
  };
}
