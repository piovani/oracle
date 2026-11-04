// Stress test: users keep growing until the API shows its limit. The thresholds are loose on
// purpose: read the report to see where the latency and the errors start.
import { pick, setup as warmUp } from './common.js';

export const options = {
  stages: [
    { duration: '1m', target: 25 },
    { duration: '1m', target: 50 },
    { duration: '1m', target: 100 },
    { duration: '1m', target: 150 },
    { duration: '30s', target: 0 },
  ],
  thresholds: { http_req_failed: ['rate<0.05'] },
};

export const setup = warmUp;

export default function (data) {
  pick(data, 0.3);
}
