package ratelimit

import (
	"fmt"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/pborman/uuid"
)

const (
	redisConcurrencyLimiterPoolMaxActive   = 1
	redisConcurrencyLimiterPoolMaxIdle     = 1
	redisConcurrencyLimiterPoolIdleTimeout = 3 * time.Minute
)

type ConcurrencyLimiter interface {
	ConcurrencyLimit(name string) (bool, error)
	Done(name string) error
}

type redisConcurrencyLimiter struct {
	pool   *redis.Pool
	prefix string
}

func NewConcurrencyLimiter(redisURL, prefix string) ConcurrencyLimiter {
	return &redisConcurrencyLimiter{
		pool: &redis.Pool{
			Dial: func() (redis.Conn, error) {
				return redis.DialURL(redisURL)
			},
			TestOnBorrow: func(c redis.Conn, _ time.Time) error {
				_, err := c.Do("PING")
				return err
			},
			MaxIdle:     redisConcurrencyLimiterPoolMaxIdle,
			MaxActive:   redisConcurrencyLimiterPoolMaxActive,
			IdleTimeout: redisConcurrencyLimiterPoolIdleTimeout,
			Wait:        true,
		},
		prefix: prefix,
	}
}

func (rl *redisConcurrencyLimiter) ConcurrencyLimit(name string) (bool, error) {
	conn := rl.pool.Get()
	defer conn.Close()

	baseKey := fmt.Sprintf("%s:%s", rl.prefix, name)
	setKey := baseKey + ":set"
	maxKey := baseKey + ":max"

	maxLimit, err := redis.Int64(conn.Do("GET", maxKey))
	if err == redis.ErrNil {
		// Limiter is disabled, let request through
		return true, nil
	} else if err != nil {
		return false, err
	}

	_, err = conn.Do("WATCH", setKey)
	if err != nil {
		return false, err
	}

	curLen, err := redis.Int64(conn.Do("SCARD", setKey))
	if err != nil {
		return false, err
	}

	if curLen >= maxLimit {
		return false, nil
	}

	connSend := func(commandName string, args ...interface{}) {
		if err != nil && err != redis.ErrNil {
			return
		}
		err = conn.Send(commandName, args...)
	}
	connSend("MULTI")
	connSend("SADD", setKey, uuid.New())
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

func (rl *redisConcurrencyLimiter) Done(name string) error {
	conn := rl.pool.Get()
	defer conn.Close()

	baseKey := fmt.Sprintf("%s:%s", rl.prefix, name)
	setKey := baseKey + ":set"
	maxKey := baseKey + ":max"

	_, err := redis.Int64(conn.Do("GET", maxKey))
	if err == redis.ErrNil {
		// Limiter is disabled, do nothing
		return nil
	}

	_, err = conn.Do("SPOP", setKey)
	return err
}
