package ratelimit

import (
	"fmt"
	"time"

	"github.com/garyburd/redigo/redis"
)

type RateLimiter interface {
	RateLimit(name string, maxCalls uint64, per time.Duration) (bool, error)
}

type redisRateLimiter struct {
	pool   *redis.Pool
	prefix string
}

type nullRateLimiter struct{}

func NewRateLimiter(redisPool *redis.Pool, prefix string) RateLimiter {
	return &redisRateLimiter{
		pool:   redisPool,
		prefix: prefix,
	}
}

func NewNullRateLimiter() RateLimiter {
	return nullRateLimiter{}
}

func (rl *redisRateLimiter) RateLimit(name string, maxCalls uint64, per time.Duration) (bool, error) {
	conn := rl.pool.Get()
	defer conn.Close()

	now := time.Now()
	timestamp := now.Unix() - (now.Unix() % int64(per.Seconds()))

	key := fmt.Sprintf("%s:%s:%d", rl.prefix, name, timestamp)

	_, err := conn.Do("WATCH", key)
	if err != nil {
		return false, err
	}

	cur, err := redis.Int64(conn.Do("GET", key))
	if err != nil && err != redis.ErrNil {
		return false, err
	}

	if err != redis.ErrNil && uint64(cur) >= maxCalls {
		return false, nil
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
