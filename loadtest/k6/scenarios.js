import http from 'k6/http';
import { check, sleep } from 'k6';
import { SharedArray } from 'k6/data';
import { thresholds } from './thresholds.js';

// Load test data from CSV files (SharedArray saves memory across virtual users)
const stopIds = new SharedArray('stop_ids', function () {
    return open('./data/stop_ids.csv').split('\n').filter(Boolean);
});
const routeIds = new SharedArray('route_ids', function () {
    return open('./data/route_ids.csv').split('\n').filter(Boolean);
});
const tripIds = new SharedArray('trip_ids', function () {
    return open('./data/trip_ids.csv').split('\n').filter(Boolean);
});
const locations = new SharedArray('locations', function () {
    return open('./data/locations.csv').split('\n').filter(Boolean);
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
        const stopId = randomItem(stopIds) || 'agency_1';
        url = `${BASE_URL}/arrivals-and-departures-for-stop/${stopId}.json?key=${API_KEY}`;
    } 
    // 25% - stops-for-location (Map browsing)
    else if (rand < 0.65) {
        const loc = randomItem(locations) || 'lat=37.7749&lon=-122.4194';
        url = `${BASE_URL}/stops-for-location.json?${loc}&key=${API_KEY}`;
    } 
    // 15% - vehicles-for-agency (Real-time polling)
    else if (rand < 0.80) {
        const agencyId = '40';  
        url = `${BASE_URL}/vehicles-for-agency/${agencyId}.json?key=${API_KEY}`;
    } 
    // 10% - trip-details
    else if (rand < 0.90) {
        const tripId = randomItem(tripIds) || 'trip_1';
        url = `${BASE_URL}/trip-details/${tripId}.json?key=${API_KEY}`;
    } 
    // 10% - Other endpoints (routes, schedules, etc.)
    else {
        const stopId = randomItem(stopIds) || 'agency_1';
        url = `${BASE_URL}/schedule-for-stop/${stopId}.json?key=${API_KEY}`;
    }

    // Execute the request
    const res = http.get(url);

    // Validate response
    check(res, {
        'status is 200': (r) => r.status === 200,
        // Ensure we aren't getting rate limited or hitting panics
        'no errors': (r) => r.status !== 500 && r.status !== 429, 
    });

    // Simulate user think-time (between 0.5 and 1.5 seconds)
    sleep(Math.random() + 0.5);
}
