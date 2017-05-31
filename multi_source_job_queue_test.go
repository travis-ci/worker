package worker

import (
	"testing"
	"time"

	gocontext "context"

	"github.com/stretchr/testify/assert"
)

func TestNewMultiSourceJobQueue(t *testing.T) {
	jq0 := &fakeJobQueue{c: make(chan Job)}
	jq1 := &fakeJobQueue{c: make(chan Job)}
	msjq := NewMultiSourceJobQueue(jq0, jq1)

	assert.NotNil(t, msjq)

	buildJobChan, err := msjq.Jobs(gocontext.TODO())
	assert.Nil(t, err)
	assert.NotNil(t, buildJobChan)

	done := make(chan struct{})

	go func() {
		<-buildJobChan
		<-buildJobChan
		done <- struct{}{}
	}()

	go func() {
		jq0.c <- &fakeJob{}
		jq1.c <- &fakeJob{}
	}()

	for {
		select {
		case <-time.After(3 * time.Second):
			assert.FailNow(t, "jobs were not received within 3s")
		case <-done:
			return
		}
	}
}

func TestMultiSourceJobQueue_Name(t *testing.T) {
	jq0 := &fakeJobQueue{c: make(chan Job)}
	jq1 := &fakeJobQueue{c: make(chan Job)}
	msjq := NewMultiSourceJobQueue(jq0, jq1)
	assert.Equal(t, "fake,fake", msjq.Name())
}

func TestMultiSourceJobQueue_Cleanup(t *testing.T) {
	jq0 := &fakeJobQueue{c: make(chan Job)}
	jq1 := &fakeJobQueue{c: make(chan Job)}
	msjq := NewMultiSourceJobQueue(jq0, jq1)
	err := msjq.Cleanup()
	assert.Nil(t, err)

	assert.True(t, jq0.cleanedUp)
	assert.True(t, jq1.cleanedUp)
}
