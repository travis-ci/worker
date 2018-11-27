package ratelimit

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRateLimit(t *testing.T) {
	if os.Getenv("REDIS_URL") == "" {
		t.Skip("skipping redis test since there is no REDIS_URL")
	}

	if time.Now().Minute() > 58 {
		t.Log("Note: The TestRateLimit test is known to have a bug if run near the top of the hour. Since the rate limiter isn't a moving window, it could end up checking against two different buckets on either side of the top of the hour, so if you see that just re-run it after you've passed the top of the hour.")
	}

	rateLimiter := NewRateLimiter(os.Getenv("REDIS_URL"), fmt.Sprintf("worker-test-rl-%d", os.Getpid()), false, time.Minute)

	ok, err := rateLimiter.RateLimit(context.TODO(), "slow", 2, time.Hour)
	if err != nil {
		t.Fatalf("rate limiter error: %v", err)
	}
	if !ok {
		t.Fatal("expected to not get rate limited, but was limited")
	}

	ok, err = rateLimiter.RateLimit(context.TODO(), "slow", 2, time.Hour)
	if err != nil {
		t.Fatalf("rate limiter error: %v", err)
	}
	if !ok {
		t.Fatal("expected to not get rate limited, but was limited")
	}

	ok, err = rateLimiter.RateLimit(context.TODO(), "slow", 2, time.Hour)
	if err != nil {
		t.Fatalf("rate limiter error: %v", err)
	}
	if ok {
		t.Fatal("expected to get rate limited, but was not limited")
	}
}
