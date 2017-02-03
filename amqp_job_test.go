package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bitly/go-simplejson"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/backend"
	gocontext "golang.org/x/net/context"
)

type fakeAMQPAcknowledger struct {
	lastAckTag    uint64
	lastAckMult   bool
	lastNackTag   uint64
	lastNackMult  bool
	lastNackReq   bool
	lastRejectTag uint64
	lastRejectReq bool
}

func (a *fakeAMQPAcknowledger) Ack(tag uint64, mult bool) error {
	a.lastAckTag = tag
	a.lastAckMult = mult
	return nil
}

func (a *fakeAMQPAcknowledger) Nack(tag uint64, mult bool, req bool) error {
	a.lastNackTag = tag
	a.lastNackMult = mult
	a.lastNackReq = req
	return nil
}

func (a *fakeAMQPAcknowledger) Reject(tag uint64, req bool) error {
	a.lastRejectTag = tag
	a.lastRejectReq = req
	return nil
}

func newTestAMQPJob(t *testing.T) *amqpJob {
	amqpConn, _ := setupConn(t)
	payload := &JobPayload{
		Type: "job:test",
		Job: JobJobPayload{
			ID:     uint64(123),
			Number: "1",
		},
		Build: BuildPayload{
			ID:     uint64(456),
			Number: "1",
		},
		UUID:   "870f986d-a88f-4801-86cc-3d2dbc6c80da",
		Config: map[string]interface{}{},
		Timeouts: TimeoutsPayload{
			HardLimit:  uint64(9000),
			LogSilence: uint64(8001),
		},
	}
	startAttributes := &backend.StartAttributes{
		Language: "go",
		Dist:     "trusty",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Error(err)
	}

	delivery := amqp.Delivery{
		Body:         body,
		Acknowledger: &fakeAMQPAcknowledger{},
	}

	rawPayload, err := simplejson.NewJson(body)
	if err != nil {
		t.Error(err)
	}

	return &amqpJob{
		conn:            amqpConn,
		delivery:        delivery,
		payload:         payload,
		rawPayload:      rawPayload,
		startAttributes: startAttributes,
	}
}

func TestAMQPJob(t *testing.T) {
	job := newTestAMQPJob(t)

	if job.Payload() == nil {
		t.Fatalf("payload not set")
	}

	if job.RawPayload() == nil {
		t.Fatalf("raw payload not set")
	}

	if job.StartAttributes() == nil {
		t.Fatalf("start attributes not set")
	}

	if job.GoString() == "" {
		t.Fatalf("go string is empty")
	}
}

func TestAMQPJob_GoString(t *testing.T) {
	job := newTestAMQPJob(t)

	str := job.GoString()

	if !strings.HasPrefix(str, "&amqpJob{") && !strings.HasSuffix(str, "}") {
		t.Fatalf("go string has unexpected format: %q", str)
	}
}

func TestAMQPJob_Error(t *testing.T) {
	job := newTestAMQPJob(t)

	err := job.Error(gocontext.TODO(), "wat")
	if err != nil {
		t.Error(err)
	}
}

func TestAMQPJob_Requeue(t *testing.T) {
	job := newTestAMQPJob(t)

	err := job.Requeue()
	if err != nil {
		t.Error(err)
	}

	acker := job.delivery.Acknowledger.(*fakeAMQPAcknowledger)
	if acker.lastAckMult {
		t.Fatalf("last ack multiple was true")
	}
}

func TestAMQPJob_Received(t *testing.T) {
	job := newTestAMQPJob(t)

	err := job.Received()
	if err != nil {
		t.Error(err)
	}
}

func TestAMQPJob_Started(t *testing.T) {
	job := newTestAMQPJob(t)

	err := job.Started()
	if err != nil {
		t.Error(err)
	}
}

func TestAMQPJob_Finish(t *testing.T) {
	job := newTestAMQPJob(t)
	ctx := gocontext.TODO()

	err := job.Finish(ctx, FinishStatePassed)
	if err != nil {
		t.Error(err)
	}
}
