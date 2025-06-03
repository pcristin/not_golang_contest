import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

// Custom metrics
const checkoutSuccess = new Counter('checkout_success');
const soldOut = new Counter('sold_out_409');
const userLimit = new Counter('user_limit_429');

// Configuration
const BASE_URL = __ENV.BASE_URL || 'https://not-golang-contest.onrender.com';

export let options = {
  stages: [
    // Minimal test for free tier (much smaller scale)
    { duration: '30s', target: 10 },     // Gentle ramp to 10 users
    { duration: '1m', target: 25 },      // Build to 25 users  
    { duration: '1m30s', target: 25 },   // Maintain steady load
    { duration: '30s', target: 40 },     // Brief peak at 40 users
    { duration: '30s', target: 0 },      // Quick ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<3000', 'p(99)<5000'],  // Acceptable for free tier
    http_req_failed: ['rate<0.5'],  // Allow many failures due to stock depletion
    checkout_success: ['count>100'],  // Much lower but achievable target
    sold_out_409: ['count>200'],     // Expect sold-out responses
  },
  // Cloud options optimized for free tier limits
  cloud: {
    projectID: 3774032,
    name: 'Flash Sale Demo - Free Tier',
    note: 'Minimal load test demonstrating flash sale system',
    distribution: {
      'amazon:us:ashburn': { loadZone: 'amazon:us:ashburn', percent: 100 },
    },
  },
};

export default function() {
  // More realistic user behavior distribution
  const behavior = Math.random();
  
  if (behavior < 0.6) {
    // 60% - Normal users (realistic flash sale behavior)
    normalUser();
  } else if (behavior < 0.85) {
    // 25% - Aggressive users (bots/power users)
    aggressiveUser();
  } else {
    // 15% - Multi-item browsers (creates interesting load patterns)
    multiItemUser();
  }
}

function normalUser() {
  // Generate realistic user and item IDs
  const userId = `k6_user_${Math.floor(Math.random() * 50000)}`;
  const itemId = Math.floor(Math.random() * 100000) + 1;
  
  // Make checkout request (no tags to reduce time series)
  const res = http.post(`${BASE_URL}/checkout?user_id=${userId}&id=${itemId}`);
  
  // Simple checks to minimize time series
  check(res, {
    'request_ok': (r) => r.status !== 0,
  });
  
  // Record metrics for better graphs
  if (res.status === 201) {
    checkoutSuccess.add(1);
    
    // 30% chance to complete purchase (realistic conversion)
    if (Math.random() < 0.3) {
      sleep(Math.random() * 2 + 1); // Realistic think time
      try {
        const body = JSON.parse(res.body);
        if (body.code) {
          http.post(`${BASE_URL}/purchase?code=${body.code}`);
        }
      } catch (e) {
        // Handle JSON parse errors gracefully
      }
    }
  } else if (res.status === 409) {
    soldOut.add(1);
  } else if (res.status === 429) {
    userLimit.add(1);
  }
  
  // Variable wait time for realistic traffic patterns
  sleep(Math.random() * 3 + 1);
}

function aggressiveUser() {
  // Bot-like behavior - creates load spikes in graphs
  const userId = `k6_bot_${Math.floor(Math.random() * 1000)}`;
  
  // Rapid-fire requests (creates interesting load patterns)
  const attempts = Math.floor(Math.random() * 3) + 2; // Reduced attempts
  
  for (let i = 0; i < attempts; i++) {
    const itemId = Math.floor(Math.random() * 100000) + 1;
    const res = http.post(`${BASE_URL}/checkout?user_id=${userId}&id=${itemId}`);
    
    if (res.status === 201) {
      checkoutSuccess.add(1);
      break; // Success, stop trying
    } else if (res.status === 409) {
      soldOut.add(1);
    } else if (res.status === 429) {
      userLimit.add(1);
      break; // Hit limit, stop
    }
    
    // Very short delay for aggressive pattern
    sleep(Math.random() * 0.2);
  }
  
  // Cool down period
  sleep(Math.random() * 1);
}

function multiItemUser() {
  // Shopping behavior - creates sustained load
  const userId = `k6_shopper_${Math.floor(Math.random() * 10000)}`;
  const itemsToCheck = Math.floor(Math.random() * 3) + 2; // Reduced items
  
  for (let i = 0; i < itemsToCheck; i++) {
    const itemId = Math.floor(Math.random() * 100000) + 1;
    const res = http.post(`${BASE_URL}/checkout?user_id=${userId}&id=${itemId}`);
    
    if (res.status === 201) {
      checkoutSuccess.add(1);
    } else if (res.status === 409) {
      soldOut.add(1);
    } else if (res.status === 429) {
      userLimit.add(1);
      break; // Hit limit, stop shopping
    }
    
    // Browse/think time between items
    sleep(Math.random() * 2 + 1);
  }
} 