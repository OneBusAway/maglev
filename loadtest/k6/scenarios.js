import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { thresholds } from './thresholds.js';
// Added for the handleSummary text output
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';

// Tell k6 not to count 4xx responses as failures.
// Only 5xx (panics, crashes) count as failures.
http.setResponseCallback(http.expectedStatuses({ min: 200, max: 499 }));

// Determine if we should skip CSVs and force fallbacks (used in CI)
const useFallbacks = __ENV.USE_FALLBACKS === 'true';

// Load test data from CSV files (SharedArray saves memory across virtual users)
const stopIds = new SharedArray('stop_ids', function () {
    return useFallbacks ? [] : open('./data/stop_ids.csv').split('\n').filter(Boolean);
});
const routeIds = new SharedArray('route_ids', function () {
    return useFallbacks ? [] : open('./data/route_ids.csv').split('\n').filter(Boolean);
});
const tripIds = new SharedArray('trip_ids', function () {
    return useFallbacks ? [] : open('./data/trip_ids.csv').split('\n').filter(Boolean);
});
const locations = new SharedArray('locations', function () {
    return useFallbacks ? [] : open('./data/locations.csv').split('\n').filter(Boolean);
});

// Configure the load test stages
export const options = {
    thresholds: thresholds,
    stages: [
        { duration: '30s', target: 50 },  // Ramp-up to 50 users
        { duration: '1m', target: 50 },   // Steady state (baseline)
        { duration: '1m', target: 200 },  // Ramp-up to 200 users (heavy load)
        { duration: '1m', target: 500 },  // Spike to 500 users (find breaking point)
        { duration: '30s', target: 0 },   // Ramp-down to 0
    ],
};

const BASE_URL = 'http://localhost:4000/api/where';
const API_KEY = 'test';

// Helper function to pick a random item from an array
function randomItem(arr) {
    if (!arr || arr.length === 0) return '';
    return arr[Math.floor(Math.random() * arr.length)].trim();
}

export default function () {
    // Determine which endpoint to hit based on realistic traffic ratios
    const rand = Math.random();
    let url = '';

    // 40% - arrivals-and-departures-for-stop (Most common)
    if (rand < 0.40) {
        // Fallback to known RABA stop
        const stopId = randomItem(stopIds) || '25_1001';
        url = `${BASE_URL}/arrivals-and-departures-for-stop/${stopId}.json?key=${API_KEY}`;
    }
    // 25% - stops-for-location (Map browsing)
    else if (rand < 0.65) {
        // Fallback to Redding, CA
        const loc = randomItem(locations) || 'lat=40.5865&lon=-122.3917';
        url = `${BASE_URL}/stops-for-location.json?${loc}&key=${API_KEY}`;
    }
    // 15% - vehicles-for-agency (Real-time polling)
    else if (rand < 0.80) {
        // Fallback to RABA agency
        const agencyId = '25';
        url = `${BASE_URL}/vehicles-for-agency/${agencyId}.json?key=${API_KEY}`;
    }
    // 10% - trip-details
    else if (rand < 0.90) {
        const tripId = randomItem(tripIds) || '25_84f4520e-88b6-4ee6-8975-856799bc1359'; 
        url = `${BASE_URL}/trip-details/${tripId}.json?key=${API_KEY}`;
    }
    // 10% - Other endpoints (routes, schedules, etc.)
    else {
        const stopId = randomItem(stopIds) || '25_1001';
        url = `${BASE_URL}/schedule-for-stop/${stopId}.json?key=${API_KEY}`;
    }

    // Execute the request
    const res = http.get(url);

    // Validate response — only check for server errors and rate limiting
    check(res, {
        'no server errors': (r) => r.status !== 500,
        'no rate limiting': (r) => r.status !== 429,
    });

    // Simulate user think-time (between 0.5 and 1.5 seconds)
    sleep(Math.random() + 0.5);
}

// Replaces the deprecated --summary-export flag
export function handleSummary(data) {
    return {
        // Output summary to stdout (console)
        'stdout': textSummary(data, { indent: ' ', enableColors: true }),
        // Output JSON report exactly where the CI expects it
        'loadtest/k6/stress-summary.json': JSON.stringify(data),
    };
}
