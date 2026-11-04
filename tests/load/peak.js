// Peak test: 10,000 simultaneous users, the day everyone opens the dashboard at once. The thresholds
// are loose on purpose: the point is to see what gives way first and whether it recovers, not a green run.
//
// A laptop is rarely enough: k6 needs memory for every user and a high open-files limit
// (ulimit -n 65535). A saturated k6 looks like a slow server, so watch its CPU too.
import { pick, setup as warmUp } from './common.js';

export const options = {
  stages: [
    { duration: '3m', target: 10000 },
    { duration: '2m', target: 10000 },
    { duration: '1m', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(95)<3000'],
  },
};

export const setup = warmUp;

export default function (data) {
  pick(data, 0.3);
}
