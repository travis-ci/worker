package worker

import (
	gocontext "context"
	"errors"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
)

// scriptedInstance is a backend.Instance test double that returns a pre-programmed sequence of
// RunScript results and counts how many times the script was (re)uploaded. Only UploadScript and
// RunScript are exercised by remintAndRerun; the remaining Instance methods are promoted from the
// embedded (nil) interface and are never called by these tests.
type scriptedInstance struct {
	backend.Instance

	runResults []*backend.RunResult
	runErrs    []error
	runIdx     int

	uploads int
}

func (i *scriptedInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	i.uploads++
	return nil
}

func (i *scriptedInstance) RunScript(ctx gocontext.Context, w io.Writer) (*backend.RunResult, error) {
	idx := i.runIdx
	i.runIdx++
	var err error
	if idx < len(i.runErrs) {
		err = i.runErrs[idx]
	}
	var res *backend.RunResult
	if idx < len(i.runResults) {
		res = i.runResults[idx]
	}
	return res, err
}

// uploadErrInstance fails every UploadScript and records whether RunScript was ever reached.
type uploadErrInstance struct {
	backend.Instance
	runIdx int
}

func (i *uploadErrInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	return errors.New("upload failed")
}

func (i *uploadErrInstance) RunScript(ctx gocontext.Context, w io.Writer) (*backend.RunResult, error) {
	i.runIdx++
	return &backend.RunResult{ExitCode: 0}, nil
}

func remintTestStep(max int) *stepRunScript {
	return &stepRunScript{
		cloneAuthRemintMax: max,
		generator: buildScriptGeneratorFunction(func(ctx gocontext.Context, job Job) ([]byte, error) {
			return []byte("#!/bin/sh\ntrue\n"), nil
		}),
	}
}

func remintTestJob() Job {
	return &fakeJob{payload: &JobPayload{Job: JobJobPayload{ID: 42}}}
}

func remintTestLogger() *logrus.Entry {
	l := logrus.New()
	l.Out = io.Discard
	return logrus.NewEntry(l)
}

const testRemintExit = cloneAuthRemintExitCode // 89

func TestRemintAndRerun_ClearsOnFirstRetry(t *testing.T) {
	s := remintTestStep(2)
	inst := &scriptedInstance{runResults: []*backend.RunResult{{ExitCode: 0}}}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	res, err := s.remintAndRerun(gocontext.Background(), remintTestJob(), inst, nil, remintTestLogger(), initial)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.ExitCode != 0 {
		t.Fatalf("expected exit 0 after retry, got %+v", res)
	}
	if inst.uploads != 1 {
		t.Fatalf("expected exactly 1 script re-upload, got %d", inst.uploads)
	}
	if inst.runIdx != 1 {
		t.Fatalf("expected exactly 1 re-run, got %d", inst.runIdx)
	}
}

func TestRemintAndRerun_ClearsOnSecondRetry(t *testing.T) {
	s := remintTestStep(2)
	inst := &scriptedInstance{runResults: []*backend.RunResult{
		{ExitCode: testRemintExit}, // first retry still 401
		{ExitCode: 1},              // second retry: real build failure -> stop
	}}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	res, err := s.remintAndRerun(gocontext.Background(), remintTestJob(), inst, nil, remintTestLogger(), initial)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.ExitCode != 1 {
		t.Fatalf("expected exit 1 after second retry, got %+v", res)
	}
	if inst.uploads != 2 || inst.runIdx != 2 {
		t.Fatalf("expected 2 re-uploads and 2 re-runs, got uploads=%d runs=%d", inst.uploads, inst.runIdx)
	}
}

func TestRemintAndRerun_ExhaustsCap(t *testing.T) {
	s := remintTestStep(2)
	inst := &scriptedInstance{runResults: []*backend.RunResult{
		{ExitCode: testRemintExit},
		{ExitCode: testRemintExit},
		{ExitCode: 0}, // must never be reached: cap is 2
	}}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	res, err := s.remintAndRerun(gocontext.Background(), remintTestJob(), inst, nil, remintTestLogger(), initial)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.ExitCode != testRemintExit {
		t.Fatalf("expected still-401 result after exhausting cap, got %+v", res)
	}
	if inst.uploads != 2 || inst.runIdx != 2 {
		t.Fatalf("expected exactly 2 attempts (cap), got uploads=%d runs=%d", inst.uploads, inst.runIdx)
	}
}

func TestRemintAndRerun_GeneratorErrorGivesUp(t *testing.T) {
	s := remintTestStep(2)
	s.generator = buildScriptGeneratorFunction(func(ctx gocontext.Context, job Job) ([]byte, error) {
		return nil, errors.New("travis-build unavailable")
	})
	inst := &scriptedInstance{runResults: []*backend.RunResult{{ExitCode: 0}}}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	res, err := s.remintAndRerun(gocontext.Background(), remintTestJob(), inst, nil, remintTestLogger(), initial)

	if err != nil {
		t.Fatalf("regeneration failure should not surface as an error, got %v", err)
	}
	if res == nil || res.ExitCode != testRemintExit {
		t.Fatalf("expected the original 401 result to be returned, got %+v", res)
	}
	if inst.uploads != 0 || inst.runIdx != 0 {
		t.Fatalf("expected no upload/run when regeneration fails, got uploads=%d runs=%d", inst.uploads, inst.runIdx)
	}
}

func TestRemintAndRerun_UploadErrorGivesUp(t *testing.T) {
	s := remintTestStep(2)
	inst := &uploadErrInstance{}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	res, err := s.remintAndRerun(gocontext.Background(), remintTestJob(), inst, nil, remintTestLogger(), initial)

	if err != nil {
		t.Fatalf("upload failure should not surface as an error, got %v", err)
	}
	if res == nil || res.ExitCode != testRemintExit {
		t.Fatalf("expected the original 401 result to be returned, got %+v", res)
	}
	if inst.runIdx != 0 {
		t.Fatalf("expected no re-run when upload fails, got %d runs", inst.runIdx)
	}
}

func TestRemintAndRerun_ContextCancelledBailsImmediately(t *testing.T) {
	s := remintTestStep(2)
	inst := &scriptedInstance{runResults: []*backend.RunResult{{ExitCode: 0}}}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	cancel()

	res, err := s.remintAndRerun(ctx, remintTestJob(), inst, nil, remintTestLogger(), initial)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.ExitCode != testRemintExit {
		t.Fatalf("expected the original 401 result when ctx is cancelled, got %+v", res)
	}
	if inst.uploads != 0 || inst.runIdx != 0 {
		t.Fatalf("expected zero attempts when ctx is cancelled, got uploads=%d runs=%d", inst.uploads, inst.runIdx)
	}
}

func TestRemintAndRerun_ZeroMaxDoesNothing(t *testing.T) {
	s := remintTestStep(0)
	inst := &scriptedInstance{runResults: []*backend.RunResult{{ExitCode: 0}}}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	res, err := s.remintAndRerun(gocontext.Background(), remintTestJob(), inst, nil, remintTestLogger(), initial)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.ExitCode != testRemintExit {
		t.Fatalf("expected the original 401 result with max=0, got %+v", res)
	}
	if inst.uploads != 0 || inst.runIdx != 0 {
		t.Fatalf("expected zero attempts with max=0, got uploads=%d runs=%d", inst.uploads, inst.runIdx)
	}
}

func TestRemintAndRerun_TransportFaultSurfacesError(t *testing.T) {
	s := remintTestStep(3)
	bang := errors.New("ssh connection lost")
	inst := &scriptedInstance{
		runResults: []*backend.RunResult{nil},
		runErrs:    []error{bang},
	}
	initial := &backend.RunResult{ExitCode: testRemintExit}

	_, err := s.remintAndRerun(gocontext.Background(), remintTestJob(), inst, nil, remintTestLogger(), initial)

	if !errors.Is(err, bang) {
		t.Fatalf("expected the transport fault to be surfaced, got %v", err)
	}
	if inst.runIdx != 1 {
		t.Fatalf("expected to stop after the first faulting run, got %d runs", inst.runIdx)
	}
}
