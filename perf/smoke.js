import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const ACTOR = __ENV.ACTOR_ID || 'platform-super-admin';
const KINDS = (__ENV.NODE_KINDS || 'identity,ci').split(',');
const REL_KINDS = (__ENV.REL_KINDS || 'depends-on,member').split(',');

// Read-only load; override with e.g. `k6 run --vus 50 --duration 2m perf/smoke.js`.
export const options = {
  stages: [
    { duration: '15s', target: 10 },
    { duration: '30s', target: 10 },
    { duration: '5s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

const params = { headers: { 'X-Actor-Id': ACTOR } };

export default function () {
  const kind = KINDS[Math.floor(Math.random() * KINDS.length)];
  const rel = REL_KINDS[Math.floor(Math.random() * REL_KINDS.length)];
  const responses = http.batch([
    ['GET', `${BASE_URL}/api/health`, null, params],
    ['GET', `${BASE_URL}/api/meta`, null, params],
    ['GET', `${BASE_URL}/api/capabilities`, null, params],
    ['GET', `${BASE_URL}/api/nodes/${kind}`, null, params],
    ['GET', `${BASE_URL}/api/relationships/${rel}`, null, params],
    ['GET', `${BASE_URL}/api/workflows`, null, params],
    ['GET', `${BASE_URL}/api/tasks`, null, params],
  ]);
  for (const res of responses) {
    check(res, { 'status is 200': (r) => r.status === 200 });
  }
  sleep(1);
}
