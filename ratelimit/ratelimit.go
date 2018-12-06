// Package ratelimit implements a rate limiter to avoid calling APIs too often.
package ratelimit

import (
	"fmt"
	"time"

	gocontext "context"

	"github.com/garyburd/redigo/redis"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/context"
	"go.opencensus.io/trace"
)

const (
	redisRateLimiterPoolMaxActive   = 1
	redisRateLimiterPoolMaxIdle     = 1
	redisRateLimiterPoolIdleTimeout = 3 * time.Minute
)

// RateLimiter checks if a call can be let through and returns true if it can.
//
// The name should be the same for all calls that should be affected by the
// same rate limit. The maxCalls and per arguments must be the same for all
// calls that use the same name, otherwise the behaviour is undefined.
//
// The rate limiter lets through maxCalls calls in a window of time specified
// by the "per" argument. Note that the window is not sliding, so if you say 10
// calls per minute, and 10 calls happen in the first second, no further calls
// will be let through for another 59 seconds.
//
// The actual call should only be made if (true, nil) is returned. If (false,
// nil) is returned, it means that the number of requests in the time window is
// met, and you should sleep for a bit and try again.
//
// In case an error happens, (false, err) is returned.
type RateLimiter interface {
	RateLimit(ctx gocontext.Context, name string, maxCalls uint64, per time.Duration) (bool, error)
}

type redisRateLimiter struct {
	pool   *redis.Pool
	prefix string

	dynamicConfig         bool
	dynamicConfigCacheTTL time.Duration

	cachedMaxCalls *uint64
	cachedDuration *time.Duration
	cacheExpiresAt *time.Time
}

type nullRateLimiter struct{}

// NewRateLimiter creates a RateLimiter that's backed by Redis. The prefix can
// be used to allow multiple rate limiters with the same name on the same Redis
// server.
func NewRateLimiter(redisURL string, prefix string, dynamicConfig bool, dynamicConfigCacheTTL time.Duration) RateLimiter {
	return &redisRateLimiter{
		pool: &redis.Pool{
			Dial: func() (redis.Conn, error) {
				return redis.DialURL(redisURL)
			},
			TestOnBorrow: func(c redis.Conn, _ time.Time) error {
				_, err := c.Do("PING")
				return err
			},
			MaxIdle:     redisRateLimiterPoolMaxIdle,
			MaxActive:   redisRateLimiterPoolMaxActive,
			IdleTimeout: redisRateLimiterPoolIdleTimeout,
			Wait:        true,
		},
		prefix: prefix,

		dynamicConfig:         dynamicConfig,
		dynamicConfigCacheTTL: dynamicConfigCacheTTL,
	}
}

// NewNullRateLimiter creates a valid RateLimiter that always lets all requests
// through immediately.
func NewNullRateLimiter() RateLimiter {
	return nullRateLimiter{}
}

// BUG(sarahhodne): The Redis rate limiter is known to let through too many
// requests when there are many clients talking to the same Redis. The reason
// for this is unknown, but it's probably wise to limit the number of clients
// to 5 or 6 for the time being.
func (rl *redisRateLimiter) RateLimit(ctx gocontext.Context, name string, maxCalls uint64, per time.Duration) (bool, error) {
	if trace.FromContext(ctx) != nil {
		var span *trace.Span
		ctx, span = trace.StartSpan(ctx, "Redis.RateLimit")
		defer span.End()
	}

	poolCheckoutStart := time.Now()

	conn := rl.pool.Get()
	defer conn.Close()

	context.TimeSince(ctx, "rate_limit_redis_pool_wait", poolCheckoutStart)

	if trace.FromContext(ctx) != nil {
		var span *trace.Span
		ctx, span = trace.StartSpan(ctx, "Redis.RateLimit.WithPool")
		defer span.End()
	}

	if rl.dynamicConfig {
		err := rl.loadDynamicConfig(ctx, conn, name, &maxCalls, &per)
		if err != nil && err != redis.ErrNil {
			return false, err
		}
	}

	now := time.Now()
	timestamp := now.Unix() - (now.Unix() % int64(per.Seconds()))

	key := fmt.Sprintf("%s:%s:%d", rl.prefix, name, timestamp)

	cur, err := redis.Int64(conn.Do("GET", key))
	if err != nil && err != redis.ErrNil {
		return false, err
	}

	if err != redis.ErrNil && uint64(cur) >= maxCalls {
		return false, nil
	}

	_, err = conn.Do("WATCH", key)
	if err != nil {
		return false, err
	}

	connSend := func(commandName string, args ...interface{}) {
		if err != nil && err != redis.ErrNil {
			return
		}
		err = conn.Send(commandName, args...)
	}
	connSend("MULTI")
	connSend("INCR", key)
	connSend("EXPIRE", key, int64(per.Seconds()))
	if err != nil {
		return false, err
	}

	reply, err := conn.Do("EXEC")
	if err != nil {
		return false, err
	}
	if reply == nil {
		return false, nil
	}

	return true, nil
}

// if dynamic config is enabled, it is possible to override
// max_calls and duration at runtime by setting redis keys:
//
// * <prefix>:<name>:max_calls
// * <prefix>:<name>:duration
//
// for example:
//
// * worker-rate-limit-gce-api-1:gce-api:max_calls 3000
// * worker-rate-limit-gce-api-1:gce-api:duration  100s
func (rl *redisRateLimiter) loadDynamicConfig(ctx gocontext.Context, conn redis.Conn, name string, maxCalls *uint64, per *time.Duration) error {
	if rl.cacheExpiresAt != nil && rl.cacheExpiresAt.Before(time.Now()) {
		rl.cachedMaxCalls = nil
		rl.cachedDuration = nil
	}

	// load from cache
	if rl.cachedMaxCalls != nil || rl.cachedDuration != nil {
		if rl.cachedMaxCalls != nil {
			*maxCalls = *rl.cachedMaxCalls
		}
		if rl.cachedDuration != nil {
			*per = *rl.cachedDuration
		}
		return nil
	}

	// load from redis
	key := fmt.Sprintf("%s:%s:max_calls", rl.prefix, name)
	dynMaxCalls, err := redis.Uint64(conn.Do("GET", key))
	if err != nil && err != redis.ErrNil {
		return err
	}
	if err != redis.ErrNil {
		*maxCalls = dynMaxCalls
	}

	key = fmt.Sprintf("%s:%s:duration", rl.prefix, name)
	dynDurationStr, err := redis.String(conn.Do("GET", key))
	if err != nil && err != redis.ErrNil {
		return err
	}
	if err != redis.ErrNil {
		dynDuration, err := time.ParseDuration(dynDurationStr)
		if err == nil {
			*per = dynDuration
		}
	}

	expires := time.Now().Add(time.Second * 10)

	logger := context.LoggerFromContext(ctx).WithField("self", "ratelimit/redis")
	logger.WithFields(logrus.Fields{
		"max_calls": *maxCalls,
		"duration":  *per,
	}).Info("refreshed dynamic config")

	rl.cachedMaxCalls = maxCalls
	rl.cachedDuration = per
	rl.cacheExpiresAt = &expires

	return nil
}

func (rl nullRateLimiter) RateLimit(ctx gocontext.Context, name string, maxCalls uint64, per time.Duration) (bool, error) {
	return true, nil
}
