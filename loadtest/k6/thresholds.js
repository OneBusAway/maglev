// loadtest/k6/thresholds.js
export const thresholds = {
    // 99% of requests must complete below 200ms
    http_req_duration: ['p(99)<200'], 
    // Error rate must be less than 1%
    http_req_failed: ['rate<0.01'],   
};
