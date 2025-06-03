import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';
import { SharedArray } from 'k6/data';

// Custom metrics
const checkoutSuccess = new Counter('checkout_success');
const purchaseSuccess = new Counter('purchase_success');
const userLimitHit = new Counter('user_limit_429');
const soldOut = new Counter('sold_out_409');
const errorRate = new Rate('errors');

// Configuration - CHANGE THIS TO YOUR SERVER URL
const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// User pool - shared across VUs
const users = new SharedArray('users', function() {
  const arr = [];
  for (let i = 0; i < 10000; i++) {
    arr.push({
      id: `k6_user_${i}`,
      checkoutCount: 0,
      codes: []
    });
  }
  return arr;
});

// Test configuration
export let options = {
  scenarios: {
    // Scenario 1: Normal checkout traffic (70%)
    normal_checkouts: {
      executor: 'constant-arrival-rate',
      rate: 7000,  // 7000 requests per second
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 500,
      maxVUs: 2000,
      exec: 'checkoutNormal',
    },
    // Scenario 2: Heavy users trying to exceed limits (10%)
    heavy_users: {
      executor: 'constant-arrival-rate',
      rate: 1000,  // 1000 requests per second
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 100,
      maxVUs: 500,
      exec: 'checkoutHeavy',
      startTime: '10s',
    },
    // Scenario 3: Purchase attempts (10%)
    purchases: {
      executor: 'constant-arrival-rate',
      rate: 1000,  // 1000 requests per second
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 100,
      maxVUs: 500,
      exec: 'makePurchase',
      startTime: '20s',  // Start after some codes are generated
    },
    // Scenario 4: Invalid requests (10%)
    invalid_requests: {
      executor: 'constant-arrival-rate',
      rate: 1000,  // 1000 requests per second
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 50,
      maxVUs: 200,
      exec: 'invalidRequests',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    http_req_failed: ['rate<0.1'],
    checkout_success: ['count>9000'],
    errors: ['rate<0.1'],
  },
};

// Shared storage for checkout codes
const checkoutCodes = [];

// Normal checkout scenario
export function checkoutNormal() {
  const user = users[Math.floor(Math.random() * users.length)];
  const itemId = Math.floor(Math.random() * 100000) + 1;
  
  const res = http.post(`${BASE_URL}/checkout?user_id=${user.id}&id=${itemId}`, null, {
    tags: { name: 'checkout_normal' }
  });
  
  const success = check(res, {
    'status is 201': (r) => r.status === 201,
  });
  
  if (res.status === 201) {
    checkoutSuccess.add(1);
    try {
      const body = JSON.parse(res.body);
      if (body.code) {
        checkoutCodes.push({
          code: body.code,
          userId: user.id,
          timestamp: Date.now()
        });
      }
    } catch (e) {}
  } else if (res.status === 409) {
    soldOut.add(1);
  } else if (res.status === 429) {
    userLimitHit.add(1);
  } else if (res.status >= 400) {
    errorRate.add(1);
  }
}

// Heavy user scenario - same users making multiple requests
export function checkoutHeavy() {
  // Pick from a smaller pool to ensure repeated users
  const heavyUserIndex = Math.floor(Math.random() * 3000);
  const user = users[heavyUserIndex];
  const itemId = Math.floor(Math.random() * 100000) + 1;
  
  const res = http.post(`${BASE_URL}/checkout?user_id=${user.id}&id=${itemId}`, null, {
    tags: { name: 'checkout_heavy' }
  });
  
  if (res.status === 201) {
    checkoutSuccess.add(1);
    try {
      const body = JSON.parse(res.body);
      if (body.code) {
        checkoutCodes.push({
          code: body.code,
          userId: user.id,
          timestamp: Date.now()
        });
      }
    } catch (e) {}
  } else if (res.status === 409) {
    soldOut.add(1);
  } else if (res.status === 429) {
    userLimitHit.add(1);
  } else if (res.status >= 400) {
    errorRate.add(1);
  }
}

// Purchase scenario
export function makePurchase() {
  if (checkoutCodes.length === 0) {
    return; // No codes available yet
  }
  
  // Try to use a recent code (80% chance) or an old one (20% chance)
  let code;
  const useRecent = Math.random() < 0.8;
  
  if (useRecent && checkoutCodes.length > 10) {
    // Use one of the last 10 codes
    const recentIndex = checkoutCodes.length - Math.floor(Math.random() * 10) - 1;
    code = checkoutCodes[recentIndex];
  } else {
    // Use any code
    code = checkoutCodes[Math.floor(Math.random() * checkoutCodes.length)];
  }
  
  if (!code) return;
  
  const res = http.post(`${BASE_URL}/purchase?code=${code.code}`, null, {
    tags: { name: 'purchase' }
  });
  
  const success = check(res, {
    'purchase successful': (r) => r.status === 200,
  });
  
  if (res.status === 200) {
    purchaseSuccess.add(1);
  } else if (res.status === 404) {
    // Expected for expired or already used codes
  } else if (res.status >= 400) {
    errorRate.add(1);
  }
}

// Invalid requests scenario
export function invalidRequests() {
  const scenarios = [
    // Missing parameters
    () => http.post(`${BASE_URL}/checkout`, null, { tags: { name: 'checkout_no_params' } }),
    () => http.post(`${BASE_URL}/checkout?user_id=test_user`, null, { tags: { name: 'checkout_missing_id' } }),
    () => http.post(`${BASE_URL}/checkout?id=123`, null, { tags: { name: 'checkout_missing_user' } }),
    () => http.post(`${BASE_URL}/purchase`, null, { tags: { name: 'purchase_no_params' } }),
    () => http.post(`${BASE_URL}/purchase?code=`, null, { tags: { name: 'purchase_empty_code' } }),
    // Invalid codes
    () => http.post(`${BASE_URL}/purchase?code=INVALID_${Math.random()}`, null, { tags: { name: 'purchase_invalid_code' } }),
  ];
  
  const scenario = scenarios[Math.floor(Math.random() * scenarios.length)];
  const res = scenario();
  
  // We expect these to fail with 4xx
  check(res, {
    'bad request returns 4xx': (r) => r.status >= 400 && r.status < 500,
  });
} 