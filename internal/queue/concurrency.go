package queue

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const ConcurrencyKey = "concurrency:leases"

// ponytail: ZSET score=expiry unix ms; cluster-wide in-flight count per 4.6/REQ-008b.
var concurrencyAcquireScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local c = redis.call('ZCARD', KEYS[1])
if c < tonumber(ARGV[2]) then
  redis.call('ZADD', KEYS[1], ARGV[3], ARGV[4])
  return 1
end
return 0
`)

func (q *Queue) ConcurrencyAcquire(ctx context.Context, member string, max int, ttl time.Duration) (bool, error) {
	if max <= 0 {
		max = 5
	}
	now := time.Now()
	res, err := concurrencyAcquireScript.Run(ctx, q.client, []string{ConcurrencyKey},
		strconv.FormatInt(now.UnixMilli(), 10),
		strconv.Itoa(max),
		strconv.FormatInt(now.Add(ttl).UnixMilli(), 10),
		member,
	).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

func (q *Queue) ConcurrencyRenew(ctx context.Context, member string, ttl time.Duration) error {
	return q.client.ZAdd(ctx, ConcurrencyKey, redis.Z{
		Score:  float64(time.Now().Add(ttl).UnixMilli()),
		Member: member,
	}).Err()
}

func (q *Queue) ConcurrencyRelease(ctx context.Context, member string) error {
	return q.client.ZRem(ctx, ConcurrencyKey, member).Err()
}

func (q *Queue) ConcurrencyCount(ctx context.Context) (int, error) {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	if err := q.client.ZRemRangeByScore(ctx, ConcurrencyKey, "-inf", now).Err(); err != nil {
		return 0, err
	}
	n, err := q.client.ZCard(ctx, ConcurrencyKey).Result()
	return int(n), err
}
