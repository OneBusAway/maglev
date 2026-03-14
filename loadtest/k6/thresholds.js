// loadtest/k6/thresholds.js

// Smoke test thresholds — used on every PR.
// Intentionally lenient: just catches panics and severe regressions.
// Note: http_req_failed only counts 5xx responses (not 404s) because
// we use http.expectedStatuses({min:200, max:499}) in smoke.js.
export const smokeThresholds = {
    // 95% of requests must complete within 300ms
    http_req_duration: ['p(95)<300'],
    // Only 5xx responses count as failures — must be 0%
    http_req_failed: ['rate<0.01'],
};

// Full stress test thresholds — used on nightly runs against main.
// These are the real SLOs for the routing engine.
export const thresholds = {
    // 99% of requests must complete below 200ms
    http_req_duration: ['p(99)<200'],
    // Error rate must be less than 1%
    http_req_failed: ['rate<0.01'],
};
