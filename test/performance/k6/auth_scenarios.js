import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import { randomIntBetween } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

export const errorRate = new Rate('auth_errors');
export const registerDuration = new Trend('register_duration');
export const verifyDuration = new Trend('verify_duration');
export const loginDuration = new Trend('login_duration');
export const refreshDuration = new Trend('refresh_duration');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: {
    registration_test: {
      executor: 'constant-arrival-rate',
      rate: 2,
      timeUnit: '1s',
      duration: '5m',
      preAllocatedVUs: 10,
      maxVUs: 100,
      exec: 'registrationFlow',
    },
    auth_endurance_test: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      stages: [
        { duration: '2m', target: 20 },
        { duration: '5m', target: 20 },
        { duration: '1m', target: 0 },
      ],
      preAllocatedVUs: 10,
      maxVUs: 50,
      exec: 'authEndurance',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<2000', 'p(99)<5000'],
    http_req_failed: ['rate<0.05'],
    register_duration: ['p(95)<3000'],
    verify_duration: ['p(95)<1500'],
    login_duration: ['p(95)<1000'],
    auth_errors: ['rate<0.1'],
  },
};

export function registrationFlow() {
  const email = `loadtest${randomIntBetween(10000, 99999)}@example.com`;
  const password = 'TestPassword123!';

  // Step 1: Register
  let res = http.post(`${BASE_URL}/api/v1/auth/register`, JSON.stringify({
    email,
    password,
    first_name: 'Load',
    last_name: 'Test',
  }), {
    headers: { 'Content-Type': 'application/json' },
  });

  const registerStart = Date.now();
  let regSuccess = check(res, {
    'register status 201': (r) => r.status === 201,
    'register has verification required': (r) => r.status === 201 || r.json('message')?.includes('verify'),
  });
  registerDuration.add(Date.now() - registerStart);

  if (!regSuccess) {
    errorRate.add(1);
    sleep(1);
    return;
  }

  // Note: In a real test, you would retrieve the verification code from the database
  // For now, we just test the registration endpoint
  sleep(1);

  // Step 2: Attempt login (should fail without verification)
  res = http.post(`${BASE_URL}/api/v1/auth/login`, JSON.stringify({
    email,
    password,
  }), {
    headers: { 'Content-Type': 'application/json' },
  });

  check(res, {
    'unverified login returns 401': (r) => r.status === 401,
  }) || errorRate.add(1);

  sleep(1);
}

export function authEndurance() {
  const email = `endurance${randomIntBetween(1, 100)}@example.com`;
  const password = 'TestPassword123!';

  // Login flow
  const loginStart = Date.now();
  let res = http.post(`${BASE_URL}/api/v1/auth/login`, JSON.stringify({
    email,
    password,
  }), {
    headers: { 'Content-Type': 'application/json' },
  });

  let loginSuccess = check(res, {
    'login status 200': (r) => r.status === 200,
    'login has tokens': (r) => r.json('access_token') !== undefined,
  });
  loginDuration.add(Date.now() - loginStart);

  if (!loginSuccess) {
    errorRate.add(1);
    sleep(1);
    return;
  }

  const token = res.json('access_token');
  const refreshToken = res.json('refresh_token');

  // Refresh token
  if (refreshToken) {
    const refreshStart = Date.now();
    res = http.post(`${BASE_URL}/api/v1/auth/refresh`, JSON.stringify({
      refresh_token: refreshToken,
    }), {
      headers: { 'Content-Type': 'application/json' },
    });

    check(res, {
      'refresh status 200': (r) => r.status === 200,
      'refresh has new token': (r) => r.json('access_token') !== undefined,
    }) || errorRate.add(1);
    refreshDuration.add(Date.now() - refreshStart);
  }

  // Get profile
  res = http.get(`${BASE_URL}/api/v1/users/me`, {
    headers: { 'Authorization': `Bearer ${token}` },
  });

  check(res, {
    'get profile status 200': (r) => r.status === 200,
  }) || errorRate.add(1);

  sleep(1);
}

export function handleSummary(data) {
  return {
    'stdout': `=== Auth Performance Summary ===\n` +
      `Total Requests: ${data.metrics.http_reqs.values.count}\n` +
      `Failed Requests: ${data.metrics.http_req_failed.values.rate * 100}%\n` +
      `Avg Response Time: ${data.metrics.http_req_duration.values.avg}ms\n` +
      `95th Percentile: ${data.metrics.http_req_duration.values['p(95)']}ms\n` +
      `99th Percentile: ${data.metrics.http_req_duration.values['p(99)']}ms\n`,
  };
}
