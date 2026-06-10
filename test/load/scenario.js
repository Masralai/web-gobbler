import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const BASE_URL = __ENV.API_URL || 'http://localhost:8080';

const scrapeErrors = new Rate('scrape_errors');
const queueTime = new Trend('queue_time_ms');
const pollIterations = new Trend('poll_iterations');

export const options = {
  scenarios: {
    ramp_load: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 50 },
        { duration: '60s', target: 50 },
        { duration: '30s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<5000'],
    scrape_errors: ['rate<0.01'],
  },
};

export default function () {
  const payload = JSON.stringify({
    url: 'https://example.com',
    extract: ['links', 'headers', 'paragraphs'],
    options: {
      timeout_seconds: 15,
      max_retries: 2,
      follow_redirects: true,
    },
  });

  const params = {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'scrape_submit' },
  };

  const submitRes = http.post(`${BASE_URL}/api/v1/scrape`, payload, params);
  const submitOk = check(submitRes, {
    'scrape submit status is 202': (r) => r.status === 202,
    'scrape submit has job_id': (r) => {
      try {
        return JSON.parse(r.body).job_id !== undefined;
      } catch (e) {
        return false;
      }
    },
  });

  if (!submitOk) {
    scrapeErrors.add(1);
    return;
  }

  const body = JSON.parse(submitRes.body);
  const pollUrl = `${BASE_URL}${body.poll_url}`;
  const startTime = Date.now();
  let polls = 0;

  for (let i = 0; i < 60; i++) {
    const pollRes = http.get(pollUrl, {
      tags: { name: 'job_poll' },
    });

    if (pollRes.status === 200) {
      try {
        const pollBody = JSON.parse(pollRes.body);
        polls++;

        if (pollBody.status === 'completed' || pollBody.status === 'failed') {
          queueTime.add(Date.now() - startTime);
          pollIterations.add(polls);

          if (pollBody.status === 'failed') {
            scrapeErrors.add(1);
          }
          return;
        }
      } catch (e) {
        // continue polling
      }
    }

    sleep(1);
  }

  scrapeErrors.add(1);
}
