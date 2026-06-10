package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const queueKey = "scraper:jobs:queue"

type JobPayload struct {
	JobID   string          `json:"job_id"`
	URL     string          `json:"url"`
	Extract []string        `json:"extract"`
	Options *PayloadOptions `json:"options,omitempty"`
}

type PayloadOptions struct {
	TimeoutSeconds  *int  `json:"timeout_seconds,omitempty"`
	MaxRetries      *int  `json:"max_retries,omitempty"`
	FollowRedirects *bool `json:"follow_redirects,omitempty"`
}

type Queue struct {
	client *redis.Client
}

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

func (q *Queue) Ping(ctx context.Context) error {
	return q.client.Ping(ctx).Err()
}

func (q *Queue) Close() error {
	return q.client.Close()
}

func (q *Queue) Client() *redis.Client {
	return q.client
}

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

func (q *Queue) QueueDepth(ctx context.Context) (int64, error) {
	depth, err := q.client.LLen(ctx, queueKey).Result()
	if err != nil {
		return 0, fmt.Errorf("llen: %w", err)
	}
	return depth, nil
}
