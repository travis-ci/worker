package main

import (
	gocontext "context"
	"fmt"
	mathrand "math/rand"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	"github.com/travis-ci/worker/ratelimit"
)

var (
	rateLimitBackoff = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worker_rate_limit_backoff_total",
		Help: "...",
	})
	apiCalls = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worker_api_calls_total",
		Help: "...",
	})
	pushgateway = push.New("http://127.0.0.1:9091", "worker-rate-limit-test").Gatherer(prometheus.DefaultGatherer)
)

func init() {
	prometheus.MustRegister(rateLimitBackoff)
	prometheus.MustRegister(apiCalls)
}

type gceProvider struct {
	rateLimiter         ratelimit.RateLimiter
	rateLimitMaxCalls   uint64
	rateLimitDuration   time.Duration
	rateLimitQueueDepth uint64
}

func (p *gceProvider) apiRateLimit(ctx gocontext.Context) error {
	metrics.Gauge("travis.worker.vm.provider.gce.rate-limit.queue", int64(p.rateLimitQueueDepth))
	startWait := time.Now()
	defer metrics.TimeSince("travis.worker.vm.provider.gce.rate-limit", startWait)

	atomic.AddUint64(&p.rateLimitQueueDepth, 1)
	defer atomic.AddUint64(&p.rateLimitQueueDepth, ^uint64(0))

	errCount := 0

	for {
		ok, err := p.rateLimiter.RateLimit("gce-api", p.rateLimitMaxCalls, p.rateLimitDuration)
		if err != nil {
			errCount++
			if errCount >= 5 {
				context.CaptureError(ctx, err)
				context.LoggerFromContext(ctx).WithFields(logrus.Fields{
					"err":  err,
					"self": "backend/gce_provider",
				}).Info("rate limiter errored 5 times")
				return err
			}
		} else {
			errCount = 0
		}
		if ok {
			return nil
		}

		rateLimitBackoff.Inc()

		// Sleep for up to 1 second
		time.Sleep(time.Millisecond * time.Duration(mathrand.Intn(1000)))
	}
}

func main() {
	ctx := gocontext.TODO()
	p := &gceProvider{
		rateLimiter:         ratelimit.NewRateLimiter("redis://", "worker-rate-limit-test"),
		rateLimitMaxCalls:   20,
		rateLimitDuration:   1 * time.Second,
		rateLimitQueueDepth: 0,
	}

	go func() {
		for {
			err := pushgateway.Push()
			if err != nil {
				panic(err)
			}
			time.Sleep(1 * time.Second)
		}
	}()

	for i := 0; i < 1000; i++ {
		go func() {
			fmt.Println("booting")
			for {
				p.apiRateLimit(ctx)
				apiCalls.Inc()
			}
		}()
	}

	time.Sleep(100 * time.Second)
}
