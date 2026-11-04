// Smoke test: one user calls every route once. Run it first, to check the API and the data.
import { routes, setup as warmUp, thresholds } from './common.js';

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: { ...thresholds, http_req_failed: ['rate==0'], checks: ['rate==1'] },
};

export const setup = warmUp;

export default function (data) {
  const r = routes(data);
  [...r.light, ...r.heavy].forEach((call) => call());
}
