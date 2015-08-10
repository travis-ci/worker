package worker

import (
	"io/ioutil"
	"os"
	"strings"

	"github.com/bitly/go-simplejson"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/metrics"
	gocontext "golang.org/x/net/context"
)

type fileJob struct {
	createdFile     string
	receivedFile    string
	startedFile     string
	finishedFile    string
	logFile         string
	bytes           []byte
	payload         *JobPayload
	rawPayload      *simplejson.Json
	startAttributes *backend.StartAttributes
}

func (j *fileJob) Payload() *JobPayload {
	return j.payload
}

func (j *fileJob) RawPayload() *simplejson.Json {
	return j.rawPayload
}

func (j *fileJob) StartAttributes() *backend.StartAttributes {
	return j.startAttributes
}

func (j *fileJob) Received() error {
	return os.Rename(j.createdFile, j.receivedFile)
}

func (j *fileJob) Started() error {
	return os.Rename(j.receivedFile, j.startedFile)
}

func (j *fileJob) Error(ctx gocontext.Context, errMessage string) error {
	log, err := j.LogWriter(ctx)
	if err != nil {
		return err
	}

	_, err = log.WriteAndClose([]byte(errMessage))
	if err != nil {
		return err
	}

	return j.Finish(FinishStateErrored)
}

func (j *fileJob) Requeue() error {
	metrics.Mark("worker.job.requeue")

	var err error

	for _, fname := range []string{
		j.receivedFile,
		j.startedFile,
		j.finishedFile,
	} {
		err = os.Rename(fname, j.createdFile)
		if err == nil {
			return nil
		}
	}

	return err
}

func (j *fileJob) Finish(state FinishState) error {
	err := os.Rename(j.startedFile, j.finishedFile)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(strings.Replace(j.finishedFile, ".json", ".state", -1),
		[]byte(state), os.FileMode(0644))
}

func (j *fileJob) LogWriter(ctx gocontext.Context) (LogWriter, error) {
	return newFileLogWriter(ctx, j.logFile)
}
