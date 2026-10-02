package rules

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore keeps the state in Redis, where every detector replica shares
// it. Each agent has two sorted sets, one of secret reads and one of web
// requests. Members are the encoded records and scores are their expiry
// times in milliseconds.
type RedisStore struct {
	client redis.Cmdable
	now    func() time.Time
}

// NewRedisStore returns a store on the given client. now supplies the scores
// and the bounds for trimming and reading, so tests advance a clock instead
// of waiting. Only the expiry of a whole key follows the server's clock.
func NewRedisStore(client redis.Cmdable, now func() time.Time) *RedisStore {
	return &RedisStore{client: client, now: now}
}

func (store *RedisStore) RecordSecretRead(ctx context.Context, customerID, agentID string, read SecretRead) ([]WebRequest, error) {
	members, err := store.record(ctx, redisKey(customerID, agentID, readsSet), redisKey(customerID, agentID, requestsSet), read, SecretReadTTL)
	if err != nil {
		return nil, err
	}
	return decodeRecords[WebRequest](members)
}

func (store *RedisStore) RecordWebRequest(ctx context.Context, customerID, agentID string, request WebRequest) ([]SecretRead, error) {
	members, err := store.record(ctx, redisKey(customerID, agentID, requestsSet), redisKey(customerID, agentID, readsSet), request, WebRequestTTL)
	if err != nil {
		return nil, err
	}
	return decodeRecords[SecretRead](members)
}

// record runs one pipeline, executed by the server in this order: add the
// record to its own set, drop that set's expired members, let the key expire
// if nothing is added again, and read the live members of the other set.
// The add comes before the read; Store explains why.
func (store *RedisStore) record(ctx context.Context, ownKey, otherKey string, record any, ttl time.Duration) ([]string, error) {
	member, err := encodeRecord(record)
	if err != nil {
		return nil, err
	}
	now := store.now()
	nowScore := strconv.FormatInt(now.UnixMilli(), 10)

	pipeline := store.client.Pipeline()
	pipeline.ZAdd(ctx, ownKey, redis.Z{Score: float64(now.Add(ttl).UnixMilli()), Member: member})
	pipeline.ZRemRangeByScore(ctx, ownKey, "-inf", nowScore)
	pipeline.Expire(ctx, ownKey, ttl)
	live := pipeline.ZRangeByScore(ctx, otherKey, &redis.ZRangeBy{Min: "(" + nowScore, Max: "+inf"}) // "(": expiry strictly after now
	if _, err := pipeline.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}
	return live.Val(), nil
}

// The two sorted sets of an agent.
const (
	readsSet    = "reads"
	requestsSet = "requests"
)

// redisKey quotes both ids, so no pair of ids can produce another pair's key.
func redisKey(customerID, agentID, set string) string {
	return fmt.Sprintf("exfiltration:%q:%q:%s", customerID, agentID, set)
}
