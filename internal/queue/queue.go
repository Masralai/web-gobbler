// Package queue provides a Redis-backed FIFO job queue using go-redis/v9.
// Jobs are pushed to the head (LPUSH) and pulled from the tail (BRPOP with a
// 5-second timeout), making it suitable for a single-worker-pool topology.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const queueKey = "scraper:jobs:queue"

// JobPayload is the message format pushed into the Redis queue.
// It carries the job ID, target URL, extract types, and optional overrides.
type JobPayload struct {
	JobID   string          `json:"job_id"`
	URL     string          `json:"url"`
	Extract []string        `json:"extract"`
	Options *PayloadOptions `json:"options,omitempty"`
}

// PayloadOptions mirrors store.JobOptions and carries per-job scraper configuration
// overrides that the worker applies before executing the scrape.
type PayloadOptions struct {
	TimeoutSeconds  *int  `json:"timeout_seconds,omitempty"`
	MaxRetries      *int  `json:"max_retries,omitempty"`
	FollowRedirects *bool `json:"follow_redirects,omitempty"`
}

// Queue wraps a Redis client and exposes FIFO enqueue/dequeue operations.
// The underlying Redis key is "scraper:jobs:queue".
type Queue struct {
	client *redis.Client
}

// New connects to Redis, verifies the connection with a ping, and returns a Queue.
func New(ctx context.Context, redisURL string) (*Queue, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	return &Queue{client: client}, nil
}

// Ping verifies the Redis connection is still alive.
func (q *Queue) Ping(ctx context.Context) error {
	return q.client.Ping(ctx).Err()
}

// Close shuts down the Redis client and releases the connection.
func (q *Queue) Close() error {
	return q.client.Close()
}

// Client returns the underlying go-redis client for advanced use cases.
func (q *Queue) Client() *redis.Client {
	return q.client
}

// Enqueue serialises the payload to JSON and pushes it to the head of the queue (LPUSH).
func (q *Queue) Enqueue(ctx context.Context, payload JobPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if err := q.client.LPush(ctx, queueKey, data).Err(); err != nil {
		return fmt.Errorf("lpush: %w", err)
	}
	return nil
}

// Dequeue blocks for up to 5 seconds waiting for a job from the tail of the queue (BRPOP).
// Returns nil, nil when the timeout fires with no job available.
func (q *Queue) Dequeue(ctx context.Context) (*JobPayload, error) {
	result, err := q.client.BRPop(ctx, 5*time.Second, queueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("brpop: %w", err)
	}

	if len(result) < 2 {
		return nil, nil
	}

	var payload JobPayload
	if err := json.Unmarshal([]byte(result[1]), &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	return &payload, nil
}

// QueueDepth returns the current number of jobs waiting in the queue (LLEN).
func (q *Queue) QueueDepth(ctx context.Context) (int64, error) {
	depth, err := q.client.LLen(ctx, queueKey).Result()
	if err != nil {
		return 0, fmt.Errorf("llen: %w", err)
	}
	return depth, nil
}
