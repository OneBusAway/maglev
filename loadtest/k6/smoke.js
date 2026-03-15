// loadtest/k6/smoke.js
// Lightweight smoke test — runs on every PR.
// Goal: verify key endpoints return 200 OK and meet baseline latency
// under minimal concurrent load (5 VUs × 30s).

import http from 'k6/http';
import { check, sleep } from 'k6';
import { smokeThresholds } from './thresholds.js';
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';

export const options = {
    thresholds: smokeThresholds,
    vus: 5,
    duration: '30s',
};

// Tell k6 not to count 4xx responses as failures.
// We only care about 5xx (panics, crashes, server errors).
// 404s are expected for some endpoints with test/expired data.
export function handleSummary(data) {
    return {
        stdout: textSummary(data, { indent: ' ', enableColors: false }),
        'loadtest/k6/smoke-summary.json': JSON.stringify(data),
    };
}

http.setResponseCallback(http.expectedStatuses({ min: 200, max: 499 }));

const BASE_URL = 'http://localhost:4000';
const API_KEY = 'test';

// RABA agency
const AGENCY_ID = '25';
// Known RABA stop
const STOP_ID = '25_1001';
// Redding, CA center
const LAT = '40.5865';
const LON = '-122.3917';

export default function () {
    const rand = Math.random();

    // Hit a spread of critical endpoints to check for panics/errors
    if (rand < 0.30) {
        // Health check — must always be 200
        const res = http.get(`${BASE_URL}/healthz`);
        check(res, {
            'healthz: status 200': (r) => r.status === 200,
        });
    } else if (rand < 0.60) {
        // Arrivals for a known stop
        const res = http.get(
            `${BASE_URL}/api/where/arrivals-and-departures-for-stop/${STOP_ID}.json?key=${API_KEY}`
        );
        check(res, {
            'arrivals-and-departures: no 5xx': (r) => r.status < 500,
        });
    } else if (rand < 0.80) {
        // Stops for a location (Redding, CA)
        const res = http.get(
            `${BASE_URL}/api/where/stops-for-location.json?lat=${LAT}&lon=${LON}&key=${API_KEY}`
        );
        check(res, {
            'stops-for-location: no 5xx': (r) => r.status < 500,
        });
    } else {
        // Routes for an agency
        const res = http.get(
            `${BASE_URL}/api/where/routes-for-agency/${AGENCY_ID}.json?key=${API_KEY}`
        );
        check(res, {
            'routes-for-agency: no 5xx': (r) => r.status < 500,
        });
    }

    sleep(Math.random() * 0.5 + 0.2);
}
