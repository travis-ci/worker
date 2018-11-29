package worker

import (
	gocontext "context"
	"github.com/garyburd/redigo/redis"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/context"
	"io/ioutil"
	"strconv"
	"strings"
	"time"
)

// ConcurrentJobLimiter checks if a new job is allowed to run or if the worker
// should wait for other jobs to finish first.
//
// This is intended to be used for infrastructures where our computing capacity
// is not flexible. In these environments, we have a hard limit on how many
// jobs we can run at once. Limiting the number of jobs dynamically in this way
// allows us to spin up extra workers (e.g. when deploying a new version)
// without worrying about exceeding our compute capacity.
type ConcurrentJobLimiter interface {
	CanRunJob(ctx gocontext.Context) (bool, error)
	CompleteJob(ctx gocontext.Context) error
	Done(ctx gocontext.Context) error
}

type redisConcurrentJobLimiter struct {
	conn              redis.Conn
	prefix            string
	name              string
	totalJobsFilePath string
}

type nullConcurrentJobLimiter struct{}

// NewConcurrentJobLimiter creates a ConcurrentJobLimiter that's backed by Redis.
// The prefix should be the same across all worker instances that are using the
// same compute pool. The name should uniquely identify this worker instance.
func NewConcurrentJobLimiter(redisURL string, prefix string, name string, totalJobsFilePath string) (ConcurrentJobLimiter, error) {
	conn, err := redis.DialURL(redisURL)
	if err != nil {
		return nil, err
	}

	key := prefix + ":" + name

	// Start with no running jobs for this worker instance.
	// Expire the value after two minutes (we will refresh it regularly).
	if _, err = conn.Do("SET", key, 0, "EX", 120); err != nil {
		return nil, err
	}

	jl := &redisConcurrentJobLimiter{
		conn:              conn,
		prefix:            prefix,
		name:              name,
		totalJobsFilePath: totalJobsFilePath,
	}

	go jl.refreshLoop()

	return jl, nil
}

// NewNullConcurrentJobLimiter creates a valid ConcurrentJobLimiter that always
// allows jobs to run.
//
// Use this for infrastructures where computing capacity is flexible and can
// generally scale with demand.
func NewNullConcurrentJobLimiter() ConcurrentJobLimiter {
	return nullConcurrentJobLimiter{}
}

func (jl *redisConcurrentJobLimiter) CanRunJob(ctx gocontext.Context) (bool, error) {
	logger := context.LoggerFromContext(ctx)

	jobsAllowed, err := jl.totalAllowedJobs(ctx)
	if err != nil {
		return false, err
	}

	jobsRunning, err := jl.totalRunningJobs(ctx)
	if err != nil {
		return false, err
	}

	allowed := jobsRunning < jobsAllowed
	logger.WithFields(logrus.Fields{
		"key":     jl.key(),
		"running": jobsRunning,
		"total":   jobsAllowed,
		"allowed": allowed,
	}).Info("checking concurrent job limiter")

	if allowed {
		// If we are allowing the job, increment the current number of jobs for
		// for this worker.
		if err = jl.incr(ctx); err != nil {
			return false, err
		}
	}

	return allowed, nil
}

func (jl *redisConcurrentJobLimiter) CompleteJob(ctx gocontext.Context) error {
	return jl.decr(ctx)
}

func (jl *redisConcurrentJobLimiter) Done(ctx gocontext.Context) error {
	_, err := jl.conn.Do("DEL", jl.key())
	return err
}

func (jl *redisConcurrentJobLimiter) refreshLoop() {
	for {
		time.Sleep(30 * time.Second)

		jl.conn.Do("EXPIRE", jl.key(), 120)
	}
}

func (jl *redisConcurrentJobLimiter) totalRunningJobs(ctx gocontext.Context) (int64, error) {
	pattern := jl.prefix + ":*"
	keys, err := redis.Values(jl.conn.Do("KEYS", pattern))
	if err != nil {
		return 0, err
	}

	values, err := redis.Int64s(jl.conn.Do("MGET", keys...))
	if err != nil {
		return 0, err
	}

	var total int64
	for _, n := range values {
		total += n
	}

	return total, nil
}

func (jl *redisConcurrentJobLimiter) totalAllowedJobs(ctx gocontext.Context) (int64, error) {
	bytes, err := ioutil.ReadFile(jl.totalJobsFilePath)
	if err != nil {
		return 0, err
	}

	str := strings.TrimSpace(string(bytes))
	return strconv.ParseInt(str, 10, 64)
}

func (jl *redisConcurrentJobLimiter) key() string {
	return jl.prefix + ":" + jl.name
}

func (jl *redisConcurrentJobLimiter) incr(ctx gocontext.Context) error {
	key := jl.key()
	jobs, err := redis.Int64(jl.conn.Do("INCR", key))
	if err == nil {
		logger := context.LoggerFromContext(ctx)
		logger.WithFields(logrus.Fields{
			"key":  key,
			"jobs": jobs,
		}).Debug("incremented running jobs")
	}
	return err
}

func (jl *redisConcurrentJobLimiter) decr(ctx gocontext.Context) error {
	key := jl.key()
	jobs, err := jl.conn.Do("DECR", key)
	if err == nil {
		logger := context.LoggerFromContext(ctx)
		logger.WithFields(logrus.Fields{
			"key":  key,
			"jobs": jobs,
		}).Debug("decremented running jobs")
	}
	return err
}

func (jl nullConcurrentJobLimiter) CanRunJob(ctx gocontext.Context) (bool, error) {
	return true, nil
}

func (jl nullConcurrentJobLimiter) CompleteJob(ctx gocontext.Context) error {
	return nil
}

func (jl nullConcurrentJobLimiter) Done(ctx gocontext.Context) error {
	return nil
}
