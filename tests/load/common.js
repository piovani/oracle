// Shared by the k6 scripts: routes, checks and the warm-up of the server.
import http from 'k6/http';
import exec from 'k6/execution';
import { check, sleep } from 'k6';

export const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
export const TICKER = __ENV.TICKER || 'PETR4';

// Every request carries kind=light or kind=heavy, so thresholds can tell them apart.
// light: one query. heavy: runs the strategy on the stock history (cached per session).
export const thresholds = {
  http_req_failed: ['rate<0.01'],
  checks: ['rate>0.99'],
  'http_req_duration{kind:light}': ['p(95)<300'],
  'http_req_duration{kind:heavy}': ['p(95)<1500'],
};

function day(offsetDays) {
  return new Date(Date.now() + offsetDays * 86400000).toISOString().slice(0, 10);
}

function get(path, name, kind) {
  const res = http.get(`${BASE_URL}${path}`, { tags: { kind, name } });
  check(res, {
    [`${name}: status 200`]: (r) => r.status === 200,
    [`${name}: JSON body`]: (r) => (r.headers['Content-Type'] || '').includes('application/json'),
  });
  return res;
}

// Routes the scripts hit. They are all read-only. /recommendations runs without analyst
// estimates and /tickers/{ticker}/estimate is left out, so the test never calls Yahoo Finance.
export function routes(data) {
  const t = data.ticker;
  return {
    light: [
      () => get('/health', '/health', 'light'),
      () => get('/sessions/latest', '/sessions/latest', 'light'),
      () => get('/strategy', '/strategy', 'light'),
      () => get('/strategy/tiers', '/strategy/tiers', 'light'),
      () => get(`/tickers/${t}`, '/tickers/{ticker}', 'light'),
      () => get(`/tickers/${t}/quotes?from=${day(-30)}`, '/tickers/{ticker}/quotes', 'light'),
      () => get(`/sessions/${data.session}/quotes`, '/sessions/{date}/quotes', 'light'),
    ],
    heavy: [
      () => get('/tickers', '/tickers', 'heavy'),
      () => get('/signals', '/signals', 'heavy'),
      () => get(`/tickers/${t}/analysis`, '/tickers/{ticker}/analysis', 'heavy'),
      () => get(`/tickers/${t}/trades`, '/tickers/{ticker}/trades', 'heavy'),
      () => get('/recommendations?estimates=false', '/recommendations', 'heavy'),
      () => get('/recommendations/tiers', '/recommendations/tiers', 'heavy'),
    ],
  };
}

// setup runs once. It stops the test when the API is down, and loads the history before the
// measurement starts: the first strategy call after a start loads three years of quotes and can
// hit the 9 s timeout, which would be measured as a failure instead of as load.
export function setup() {
  const health = http.get(`${BASE_URL}/health`);
  if (health.status !== 200) {
    exec.test.abort(`API not reachable at ${BASE_URL} (status ${health.status}). Start it with: go run ./cmd/oracle api`);
  }
  const latest = http.get(`${BASE_URL}/sessions/latest`);
  if (latest.status !== 200) {
    exec.test.abort(`GET /sessions/latest answered ${latest.status}: run make sync first`);
  }
  for (const path of ['/signals', '/tickers']) {
    for (let attempt = 0; attempt < 3; attempt++) {
      if (http.get(`${BASE_URL}${path}`, { timeout: '60s' }).status === 200) break;
      sleep(2);
    }
  }
  return { ticker: TICKER, session: latest.json('date') };
}

// pick runs one random route: heavyShare is the fraction of iterations on heavy routes.
export function pick(data, heavyShare) {
  const r = routes(data);
  const group = Math.random() < heavyShare ? r.heavy : r.light;
  group[Math.floor(Math.random() * group.length)]();
  sleep(0.5 + Math.random());
}
