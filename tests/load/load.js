// Load test: a steady crowd of users, 70% on light routes and 30% on heavy ones.
import { pick, setup as warmUp, thresholds } from './common.js';

export const options = {
  stages: [
    { duration: '2m', target: 1000 },
    { duration: '2m', target: 1000 },
    { duration: '30s', target: 0 },
  ],
  thresholds,
};

export const setup = warmUp;

export default function (data) {
  pick(data, 0.3);
}
