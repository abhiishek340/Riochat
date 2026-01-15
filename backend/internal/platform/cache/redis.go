package cache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
)

var slidingScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
if redis.call('ZCARD', KEYS[1]) >= limit then
  return 0
end
redis.call('ZADD', KEYS[1], now, ARGV[5])
redis.call('PEXPIRE', KEYS[1], ttl)
return 1
`)

var fixedScript = redis.NewScript(`
local limit = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local ok = 1
for i = 1, #KEYS do
  local c = redis.call('INCR', KEYS[i])
  if c == 1 or redis.call('PTTL', KEYS[i]) < 0 then
    redis.call('PEXPIRE', KEYS[i], ttl)
  end
  if c > limit then ok = 0 end
end
return ok
`)

var incrScript = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return c
`)

// Redis is the Redis backend.
type Redis struct {
	client *redis.Client
	clock  clock.Clock
}

// NewRedis wraps an existing client (tests, custom wiring).
func NewRedis(client *redis.Client, clk clock.Clock) *Redis {
	if clk == nil {
		clk = clock.System{}
	}
	return &Redis{client: client, clock: clk}
}

// OpenRedis connects using cfg and pings with a 2 second timeout; a failed
// ping is an error (startup fails).
func OpenRedis(ctx context.Context, cfg config.RedisConfig, clk clock.Clock) (*Redis, error) {
	opts := &redis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLSEnabled {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSInsecureSkipVerify} //nolint:gosec // operator opt-in
	}
	r := NewRedis(redis.NewClient(opts), clk)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := r.Ping(pingCtx); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("redis ping %s: %w", cfg.Addr, err)
	}
	return r, nil
}

// Name implements Cache.
func (r *Redis) Name() string { return "redis" }

// Ping implements Cache.
func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

// Close implements Cache.
func (r *Redis) Close() error { return r.client.Close() }

// Get implements KV.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

// Set implements KV.
func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttlOrDefault(ttl)).Err()
}

// SetNX implements KV.
func (r *Redis) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return r.client.SetNX(ctx, key, value, ttlOrDefault(ttl)).Result()
}

// GetDel implements KV.
func (r *Redis) GetDel(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := r.client.GetDel(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

// Delete implements KV.
func (r *Redis) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.client.Del(ctx, keys...).Err()
}

// Incr implements KV.
func (r *Redis) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	return incrScript.Run(ctx, r.client, []string{key}, ttlOrDefault(ttl).Milliseconds()).Int64()
}

// AllowSliding implements RateLimiter.
func (r *Redis) AllowSliding(ctx context.Context, key string, limit int, window, ttl time.Duration) (bool, error) {
	if key == "" || limit <= 0 {
		return true, nil
	}
	if window <= 0 {
		window = time.Minute
	}
	if ttl <= 0 {
		ttl = 2 * window
	}
	now := r.clock.Now().UnixMilli()
	res, err := slidingScript.Run(ctx, r.client, []string{key},
		now, window.Milliseconds(), limit, ttl.Milliseconds(), strconv.FormatInt(now, 10)+"-"+uuid.NewString()).Int64()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

// AllowFixed implements RateLimiter. In cluster deployments the keys must
// share a hash tag so they live in one slot.
func (r *Redis) AllowFixed(ctx context.Context, keys []string, limit int, ttl time.Duration) (bool, error) {
	if len(keys) == 0 || limit <= 0 {
		return true, nil
	}
	res, err := fixedScript.Run(ctx, r.client, keys, limit, ttlOrDefault(ttl).Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}
