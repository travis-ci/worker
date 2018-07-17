// Package ratelimit implements a rate limiter to avoid calling APIs too often.
package ratelimit

import (
	"fmt"
	"time"

	"github.com/garyburd/redigo/redis"
)

const (
	redisRateLimiterPoolMaxActive   = 1
	redisRateLimiterPoolMaxIdle     = 1
	redisRateLimiterPoolIdleTimeout = 3 * time.Minute
)

type RateLimiter interface {
	// RateLimit checks if a call can be let through and returns true if it can.
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
	RateLimit(name string, maxCalls uint64, per time.Duration) (bool, error)
}

type redisRateLimiter struct {
	pool   *redis.Pool
	prefix string
}

type nullRateLimiter struct{}

// NewRateLimiter creates a RateLimiter that's backed by Redis. The prefix can
// be used to allow multiple rate limiters with the same name on the same Redis
// server.
func NewRateLimiter(redisURL string, prefix string) RateLimiter {
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
func (rl *redisRateLimiter) RateLimit(name string, maxCalls uint64, per time.Duration) (bool, error) {
	conn := rl.pool.Get()
	defer conn.Close()

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

func (rl nullRateLimiter) RateLimit(name string, maxCalls uint64, per time.Duration) (bool, error) {
	return true, nil
}
