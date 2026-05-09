/*
 * k6 Load Testing Script for GopherProxy
 *
 * Scenarios:
 *  - smoke:  10 users, 30s  (quick sanity check)
 *  - load:   gradual ramp to 500 users over 2min, sustain for 3min (production-like)
 *  - spike:  sudden spike from 10 → 500 users (stress test)
 *  - sustained: 200 concurrent users for 5 minutes (sustained throughput)
 *
 * Usage:
 *  k6 run k6-load-test.js --scenario smoke
 *  k6 run k6-load-test.js --scenario load
 *  k6 run k6-load-test.js --scenario spike
 *  k6 run k6-load-test.js --scenario sustained
 */

import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate, Trend, Counter, Gauge } from 'k6/metrics';

// Custom metrics
const errorRate = new Rate('errors');
const rateLimitedRatio = new Rate('rate_limited');
const requestLatency = new Trend('request_latency', { unit: 'ms' });
const successCount = new Counter('success_count');
const rateLimitCount = new Counter('rate_limit_count');
const activeConnections = new Gauge('active_connections');

// Configuration
const PROXY_URL = __ENV.PROXY_URL || 'http://localhost:8080';
const REQUEST_TIMEOUT = '10s';

export const options = {
  scenarios: {
    smoke: {
      executor: 'constant-vus',
      vus: 10,
      duration: '30s',
      gracefulStop: '5s',
    },
    load: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '1m', target: 100 },   // ramp up to 100 over 1 min
        { duration: '1m', target: 300 },   // ramp to 300 over 1 min
        { duration: '30s', target: 500 },  // spike to 500 over 30s
        { duration: '3m', target: 500 },   // sustain at 500 for 3 min
        { duration: '1m', target: 0 },     // ramp down over 1 min
      ],
      gracefulStop: '10s',
    },
    spike: {
      executor: 'ramping-vus',
      startVUs: 10,
      stages: [
        { duration: '10s', target: 10 },
        { duration: '5s', target: 500 },    // sudden spike
        { duration: '2m', target: 500 },    // sustain
        { duration: '1m', target: 0 },
      ],
      gracefulStop: '10s',
    },
    sustained: {
      executor: 'constant-vus',
      vus: 200,
      duration: '5m',
      gracefulStop: '10s',
    },
    // Arrival-rate scenarios (target RPS)
    rps200: {
      executor: 'constant-arrival-rate',
      rate: 200,           // 200 iterations per second
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 50,
      maxVUs: 500,
    },
    rps4200: {
      executor: 'constant-arrival-rate',
      rate: 4200,          // 4200 iterations per second (requires distributed clients)
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 400,
      maxVUs: 2000,
    },
  },
};

export default function () {
  // Track concurrent connections
  activeConnections.add(1);

  // Test basic proxy functionality
  group('proxy requests', () => {
    const res = http.get(PROXY_URL, {
      timeout: REQUEST_TIMEOUT,
      tags: { name: 'ProxyRequest' },
    });

    // Record latency
    requestLatency.add(res.timings.duration);

    // Check response status
    const isSuccess = res.status === 200;
    const isRateLimited = res.status === 429;

    check(res, {
      'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
      'response time < 1s': (r) => r.timings.duration < 1000,
      'response time < 500ms': (r) => r.timings.duration < 500,
    });

    // Track metrics
    if (isSuccess) {
      successCount.add(1);
      errorRate.add(false);
    } else if (isRateLimited) {
      rateLimitCount.add(1);
      rateLimitedRatio.add(true);
      errorRate.add(false);
    } else {
      errorRate.add(true);
    }
  });

  // Simulate some think time between requests
  sleep(Math.random() * 0.5 + 0.1);

  activeConnections.add(-1);
}

export function handleSummary(data) {
  // Custom summary
  console.log('\n');
  console.log('═══════════════════════════════════════════════════════════');
  console.log('               GopherProxy k6 Load Test Summary');
  console.log('═══════════════════════════════════════════════════════════');

  return {
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
    'summary.json': JSON.stringify(data),
  };
}

// Simple text summary formatter
function textSummary(data, options) {
  if (!data.metrics) return '';

  let summary = '';
  const indent = options.indent || '';

  // Extract key metrics from the metrics object
  const metrics = data.metrics;

  for (const [name, metric] of Object.entries(metrics)) {
    if (metric.values) {
      summary += `\n${indent}${name}:\n`;

      if (metric.type === 'trend') {
        summary += `${indent}  min: ${metric.values.min?.toFixed(2)}ms\n`;
        summary += `${indent}  avg: ${metric.values.avg?.toFixed(2)}ms\n`;
        summary += `${indent}  med: ${metric.values.med?.toFixed(2)}ms\n`;
        summary += `${indent}  p95: ${metric.values['p(95)']?.toFixed(2)}ms\n`;
        summary += `${indent}  p99: ${metric.values['p(99)']?.toFixed(2)}ms\n`;
        summary += `${indent}  max: ${metric.values.max?.toFixed(2)}ms\n`;
      } else if (metric.type === 'rate') {
        summary += `${indent}  ${(metric.values.rate * 100).toFixed(2)}%\n`;
      } else if (metric.type === 'counter') {
        summary += `${indent}  ${metric.values.value?.toFixed(0)}\n`;
      }
    }
  }

  return summary;
}
