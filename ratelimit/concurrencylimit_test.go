package ratelimit

import (
	"fmt"
	"os"
	"testing"

	"github.com/garyburd/redigo/redis"
)

func TestConcurrencyLimit(t *testing.T) {
	if os.Getenv("REDIS_URL") == "" {
		t.Skip("skipping redis test since there is no REDIS_URL")
	}

	redisConn, err := redis.DialURL(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}

	prefix := fmt.Sprintf("worker-test-cl-%d", os.Getpid())
	concurrencyLimiter := NewConcurrencyLimiter(os.Getenv("REDIS_URL"), prefix)

	_, err = redisConn.Do("SET", fmt.Sprintf("%s:test-cl:enable", prefix), true)
	if err != nil {
		t.Fatal(err)
	}

	// First start two "processes"
	ok, err := concurrencyLimiter.ConcurrencyLimit("test-cl", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected not to get limited, but was")
	}
	ok, err = concurrencyLimiter.ConcurrencyLimit("test-cl", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected not to get limited, but was")
	}

	// Now the 3rd one should be limited, because we set the max to 2
	ok, err = concurrencyLimiter.ConcurrencyLimit("test-cl", 2)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected to get limited, but wasn't")
	}

	// Mark one of the processes as done
	err = concurrencyLimiter.Done("test-cl")
	if err != nil {
		t.Fatal(err)
	}

	// Now we shouldn't be limited again, since there should only be one process left
	ok, err = concurrencyLimiter.ConcurrencyLimit("test-cl", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected not to get limited, but was")
	}
}
