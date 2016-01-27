package ratelimit

import (
	"fmt"
	"sync"
	"time"

	"github.com/garyburd/redigo/redis"
)

type RateLimiter interface {
	RateLimit(name string, maxCalls uint64, per time.Duration) (bool, error)
}

type redisRateLimiter struct {
	c      redis.Conn
	prefix string

	mutex sync.Mutex
}

type nullRateLimiter struct{}

func NewRateLimiter(c redis.Conn, prefix string) RateLimiter {
	return &redisRateLimiter{
		c:      c,
		prefix: prefix,
	}
}

func NewNullRateLimiter() RateLimiter {
	return nullRateLimiter{}
}

func (rl *redisRateLimiter) RateLimit(name string, maxCalls uint64, per time.Duration) (bool, error) {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	now := time.Now()
	timestamp := now.Unix() - (now.Unix() % int64(per.Seconds()))

	key := fmt.Sprintf("%s:%s:%d", rl.prefix, name, timestamp)

	_, err := rl.c.Do("WATCH", key)

	cur, err := redis.Int64(rl.c.Do("GET", key))
	if err != nil && err != redis.ErrNil {
		return false, err
	}

	if err != redis.ErrNil && uint64(cur) >= maxCalls {
		return false, nil
	}

	rl.c.Send("MULTI")
	rl.c.Send("INCR", key)
	rl.c.Send("EXPIRE", key, int64(per.Seconds()))
	reply, err := rl.c.Do("EXEC")
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
