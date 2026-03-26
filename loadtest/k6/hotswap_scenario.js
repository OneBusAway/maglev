/**
 * Hot-Swap Load Test Scenario for Maglev
 * 
 * This scenario simulates steady production load while measuring the impact
 * of a GTFS static data refresh (ForceUpdate / hot-swap).
 * 
 * Usage:
 *   k6 run loadtest/k6/hotswap_scenario.js
 * 
 * The test maintains a steady baseline load, then you trigger ForceUpdate
 * (either programmatically or via the perftest). The test measures:
 *   - Request latency percentiles (p95, p99)
 *   - Error rates during the swap window
 *   - Request throughput degradation
 */

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import { SharedArray } from 'k6/data';

// Custom metrics for hot-swap analysis
const requestsDuringSwap = new Counter('requests_during_swap');
const errorsDuringSwap = new Counter('errors_during_swap');
const swapWindowErrors = new Rate('swap_window_error_rate');
const latencyP99 = new Trend('latency_p99', true);

// Load test data
const stopIds = new SharedArray('stop_ids', function () {
    try {
        return open('./data/stop_ids.csv').split('\n').filter(Boolean);
    } catch (e) {
        return ['25_1'];  // Fallback for RABA
    }
});

const routeIds = new SharedArray('route_ids', function () {
    try {
        return open('./data/route_ids.csv').split('\n').filter(Boolean);
    } catch (e) {
        return ['25_1'];
    }
});

// Configuration for hot-swap testing
export const options = {
    scenarios: {
        // Steady baseline load
        steady_load: {
            executor: 'constant-arrival-rate',
            rate: 100,           // 100 requests per second
            timeUnit: '1s',
            duration: '5m',      // Run for 5 minutes
            preAllocatedVUs: 50,
            maxVUs: 200,
        },
    },
    thresholds: {
        // Standard thresholds
        'http_req_duration': ['p(95)<500', 'p(99)<1000'],
        'http_req_failed': ['rate<0.01'],
        
        // Hot-swap specific thresholds
        'swap_window_error_rate': ['rate<0.05'],  // <5% errors during swap
        'latency_p99': ['p(99)<2000'],            // p99 under 2s even during swap
    },
};

const BASE_URL = __ENV.MAGLEV_URL || 'http://localhost:4000';
const API_KEY = __ENV.API_KEY || 'test';

// State tracking for swap window detection
// NOTE: In k6, each Virtual User (VU) runs in an isolated JavaScript environment.
// Module-level variables like swapStarted are NOT shared across VUs.
// Therefore, swap window detection is per-VU. Only the specific VU that
// experiences the latency spike will track these metrics. Global metrics like
// requests_during_swap and swap_window_error_rate will be underreported.
let swapStarted = false;
let swapStartTime = 0;
const SWAP_WINDOW_MS = 30000;  // 30 second window around swap

function randomItem(arr) {
    if (!arr || arr.length === 0) return '';
    return arr[Math.floor(Math.random() * arr.length)].trim();
}

function detectSwapWindow(latencyMs) {
    // Heuristic: if latency suddenly spikes 10x baseline, swap might be in progress
    const BASELINE_LATENCY = 50;  // Expected baseline in ms
    const SPIKE_THRESHOLD = 10;
    
    if (!swapStarted && latencyMs > BASELINE_LATENCY * SPIKE_THRESHOLD) {
        swapStarted = true;
        swapStartTime = Date.now();
        console.log(`[SWAP DETECTED] Latency spike at ${new Date().toISOString()}`);
    }
    
    // Check if we're in the swap window
    if (swapStarted) {
        const elapsed = Date.now() - swapStartTime;
        if (elapsed < SWAP_WINDOW_MS) {
            return true;
        } else if (latencyMs < BASELINE_LATENCY * 2) {
            // Latency normalized, swap window ended
            swapStarted = false;
            console.log(`[SWAP ENDED] Normal latency restored at ${new Date().toISOString()}`);
        }
    }
    
    return false;
}

export default function () {
    // Mix of realistic API calls
    const rand = Math.random();
    let url = '';
    let endpoint = '';

    if (rand < 0.40) {
        const stopId = randomItem(stopIds) || '25_1';
        endpoint = 'arrivals-and-departures-for-stop';
        url = `${BASE_URL}/api/where/${endpoint}/${stopId}.json?key=${API_KEY}`;
    } else if (rand < 0.65) {
        endpoint = 'stops-for-location';
        // Portland coordinates for TriMet, Redding for RABA fallback
        const lat = __ENV.TEST_LAT || '45.52';
        const lon = __ENV.TEST_LON || '-122.68';
        url = `${BASE_URL}/api/where/${endpoint}.json?lat=${lat}&lon=${lon}&radius=2000&key=${API_KEY}`;
    } else if (rand < 0.80) {
        endpoint = 'vehicles-for-agency';
        const agencyId = __ENV.AGENCY_ID || '40';
        url = `${BASE_URL}/api/where/${endpoint}/${agencyId}.json?key=${API_KEY}`;
    } else if (rand < 0.90) {
        endpoint = 'current-time';
        url = `${BASE_URL}/api/where/${endpoint}.json?key=${API_KEY}`;
    } else {
        endpoint = 'agencies-with-coverage';
        url = `${BASE_URL}/api/where/${endpoint}.json?key=${API_KEY}`;
    }

    const startTime = Date.now();
    const res = http.get(url, {
        timeout: '10s',
        tags: { endpoint: endpoint },
    });
    const latencyMs = Date.now() - startTime;

    // Track p99 latency
    latencyP99.add(latencyMs);

    // Detect if we're in a swap window
    const inSwapWindow = detectSwapWindow(latencyMs);
    
    // Check response
    const success = check(res, {
        'status is 200': (r) => r.status === 200,
        'no server error': (r) => r.status !== 500,
        'not rate limited': (r) => r.status !== 429,
        'response has data': (r) => {
            try {
                const body = JSON.parse(r.body);
                return body.code === 200 || body.data !== undefined;
            } catch (e) {
                return false;
            }
        },
    });

    // Track swap window metrics
    if (inSwapWindow) {
        requestsDuringSwap.add(1);
        if (!success) {
            errorsDuringSwap.add(1);
        }
        swapWindowErrors.add(!success);
    }

    // Brief think time
    sleep(Math.random() * 0.5 + 0.1);
}

export function handleSummary(data) {
    const summary = {
        timestamp: new Date().toISOString(),
        duration_seconds: data.state.testRunDurationMs / 1000,
        requests_total: data.metrics.http_reqs.values.count,
        requests_per_second: data.metrics.http_reqs.values.rate,
        latency_avg_ms: data.metrics.http_req_duration.values.avg,
        latency_p95_ms: data.metrics.http_req_duration.values['p(95)'],
        latency_p99_ms: data.metrics.http_req_duration.values['p(99)'],
        error_rate: data.metrics.http_req_failed.values.rate,
    };
    
    // Add swap window metrics if available
    if (data.metrics.requests_during_swap) {
        summary.requests_during_swap = data.metrics.requests_during_swap.values.count;
        summary.errors_during_swap = data.metrics.errors_during_swap 
            ? data.metrics.errors_during_swap.values.count 
            : 0;
        summary.swap_window_error_rate = data.metrics.swap_window_error_rate
            ? data.metrics.swap_window_error_rate.values.rate
            : 0;
    }

    console.log('\n=== HOT-SWAP LOAD TEST SUMMARY ===');
    console.log(JSON.stringify(summary, null, 2));

    return {
        'stdout': JSON.stringify(summary, null, 2) + '\n',
        'loadtest/results/hotswap_summary.json': JSON.stringify(summary, null, 2),
    };
}
