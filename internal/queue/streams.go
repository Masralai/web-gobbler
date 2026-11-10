package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	StreamDefault = "jobs:default"
	StreamBrowser = "jobs:browser"
	StreamWebhook = "jobs:webhook"
	StreamDead    = "jobs:dead"
)

type StreamPayload struct {
	JobID    string `json:"job_id"`
	Kind     string `json:"kind"`
	ParentID string `json:"parent_id,omitempty"`
	URL      string `json:"url,omitempty"`
	Depth    int    `json:"depth,omitempty"`
}

type StreamMessage struct {
	ID      string
	Payload StreamPayload
}

func (q *Queue) EnsureGroup(ctx context.Context, stream, group string) error {
	err := q.client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create group %s/%s: %w", stream, group, err)
	}
	return nil
}

func (q *Queue) PublishStream(ctx context.Context, stream string, payload StreamPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: 10000,
		Approx: true,
		Values: map[string]any{"payload": string(data)},
	}).Err()
}

func (q *Queue) ReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) ([]StreamMessage, error) {
	if count < 1 {
		count = 1
	}
	res, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []StreamMessage
	for _, s := range res {
		for _, m := range s.Messages {
			raw, _ := m.Values["payload"].(string)
			var p StreamPayload
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				continue
			}
			out = append(out, StreamMessage{ID: m.ID, Payload: p})
		}
	}
	return out, nil
}

func (q *Queue) Ack(ctx context.Context, stream, group string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return q.client.XAck(ctx, stream, group, ids...).Err()
}

func (q *Queue) AutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int64) ([]StreamMessage, error) {
	res, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Start:    "0-0",
		Count:    count,
	}).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	var out []StreamMessage
	for _, m := range res {
		raw, _ := m.Values["payload"].(string)
		var p StreamPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			continue
		}
		out = append(out, StreamMessage{ID: m.ID, Payload: p})
	}
	return out, nil
}

func (q *Queue) DeadLetter(ctx context.Context, stream string, payload StreamPayload, reason string) error {
	data, err := json.Marshal(map[string]any{"stream": stream, "reason": reason, "payload": payload})
	if err != nil {
		return err
	}
	return q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamDead,
		Values: map[string]any{"payload": string(data)},
	}).Err()
}

func (q *Queue) StreamDepth(ctx context.Context, stream string) (int64, error) {
	return q.client.XLen(ctx, stream).Result()
}
