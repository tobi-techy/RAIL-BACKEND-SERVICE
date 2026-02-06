import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import { randomString, randomIntBetween } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

export const errorRate = new Rate('errors');
export const loginDuration = new Trend('login_duration');
export const registerDuration = new Trend('register_duration');
export const depositDuration = new Trend('deposit_duration');
export const portfolioDuration = new Trend('portfolio_duration');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || '';

export const options = {
  scenarios: {
    smoke_test: {
      executor: 'constant-arrival-rate',
      rate: 1,
      timeUnit: '1s',
      duration: '10s',
      preAllocatedVUs: 1,
      maxVUs: 5,
      exec: 'smokeTest',
    },
    load_test: {
      executor: 'ramping-arrival-rate',
      startRate: 10,
      timeUnit: '1s',
      stages: [
        { duration: '30s', target: 50 },
        { duration: '1m', target: 100 },
        { duration: '30s', target: 200 },
        { duration: '1m', target: 200 },
        { duration: '30s', target: 50 },
        { duration: '30s', target: 0 },
      ],
      preAllocatedVUs: 10,
      maxVUs: 200,
      exec: 'loadTest',
    },
    stress_test: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      stages: [
        { duration: '1m', target: 100 },
        { duration: '2m', target: 500 },
        { duration: '2m', target: 1000 },
        { duration: '1m', target: 0 },
      ],
      preAllocatedVUs: 50,
      maxVUs: 1000,
      exec: 'stressTest',
    },
    soak_test: {
      executor: 'constant-arrival-rate',
      rate: 100,
      timeUnit: '1s',
      duration: '30m',
      preAllocatedVUs: 50,
      maxVUs: 500,
      exec: 'soakTest',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    http_req_failed: ['rate<0.01'],
    login_duration: ['p(95)<1000'],
    register_duration: ['p(95)<2000'],
    deposit_duration: ['p(95)<1500'],
    portfolio_duration: ['p(95)<500'],
    errors: ['rate<0.05'],
  },
};

export function smokeTest() {
  const res = http.get(`${BASE_URL}/health`);
  check(res, { 'health endpoint works': (r) => r.status === 200 });
  sleep(1);
}

export function loadTest() {
  runCommonScenarios();
}

export function stressTest() {
  runCommonScenarios();
}

export function soakTest() {
  runCommonScenarios();
}

function runCommonScenarios() {
  // Health check (no auth required)
  let res = http.get(`${BASE_URL}/health`);
  check(res, { 'health check status 200': (r) => r.status === 200 }) || errorRate.add(1);

  // Ready endpoint
  res = http.get(`${BASE_URL}/ready`);
  check(res, { 'ready check status 200': (r) => r.status === 200 }) || errorRate.add(1);

  // Login endpoint (rate limited)
  res = http.post(`${BASE_URL}/api/v1/auth/login`, JSON.stringify({
    email: `test${randomIntBetween(1, 1000)}@example.com`,
    password: 'TestPassword123!',
  }), {
    headers: { 'Content-Type': 'application/json' },
  });

  const loginStart = Date.now();
  let loginSuccess = check(res, {
    'login status 200 or 401': (r) => r.status === 200 || r.status === 401,
    'login has response time': (r) => r.timings.duration > 0,
  }) || errorRate.add(1);
  loginDuration.add(Date.now() - loginStart);

  if (res.status === 200) {
    const token = res.json('access_token');

    // Get profile
    res = http.get(`${BASE_URL}/api/v1/users/me`, {
      headers: { 'Authorization': `Bearer ${token}` },
    });
    check(res, { 'get profile status 200': (r) => r.status === 200 }) || errorRate.add(1);

    // Get balances
    res = http.get(`${BASE_URL}/api/v1/balances`, {
      headers: { 'Authorization': `Bearer ${token}` },
    });
    const portfolioStart = Date.now();
    check(res, { 'get balances status 200': (r) => r.status === 200 }) || errorRate.add(1);
    portfolioDuration.add(Date.now() - portfolioStart);

    // Get portfolio overview
    res = http.get(`${BASE_URL}/api/v1/portfolio/overview`, {
      headers: { 'Authorization': `Bearer ${token}` },
    });
    check(res, { 'get portfolio status 200': (r) => r.status === 200 }) || errorRate.add(1);

    sleep(1);
  }
}

export function handleSummary(data) {
  return {
    'stdout': textSummary(data, { indent: ' ', enableColors: true }),
    [`performance/reports/k6-report-${Date.now()}.json`]: JSON.stringify(data, null, 2),
  };
}
