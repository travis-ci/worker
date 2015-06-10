package workerintegration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/streadway/amqp"
)

type LogPart struct {
	ID      int64  `json:"id"`
	Content string `json:"log"`
	Final   bool   `json:"final"`
	UUID    string `json:"uuid"`
	Number  int64  `json:"number"`
}

type LogPartSlice []LogPart

func (lps LogPartSlice) Len() int {
	return len(lps)
}

func (lps LogPartSlice) Less(i, j int) bool {
	return lps[i].Number < lps[j].Number
}

func (lps LogPartSlice) Swap(i, j int) {
	lps[i], lps[j] = lps[j], lps[i]
}

type StateUpdate struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

func setupConn() (*amqp.Connection, error) {
	amqpConn, err := amqp.Dial(os.Getenv("AMQP_URL"))
	if err != nil {
		return nil, err
	}

	amqpChan, err := amqpConn.Channel()
	if err != nil {
		return nil, err
	}
	defer amqpChan.Close()

	_, err = amqpChan.QueueDeclare("builds.test", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	_, err = amqpChan.QueueDeclare("reporting.jobs.logs", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	_, err = amqpChan.QueueDeclare("reporting.jobs.builds", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	_, err = amqpChan.QueuePurge("builds.test", false)
	if err != nil {
		return nil, err
	}

	_, err = amqpChan.QueuePurge("reporting.jobs.logs", false)
	if err != nil {
		return nil, err
	}

	_, err = amqpChan.QueuePurge("reporting.jobs.builds", false)
	if err != nil {
		return nil, err
	}

	return amqpConn, nil
}

func publishJob(amqpConn *amqp.Connection) error {
	amqpChan, err := amqpConn.Channel()
	if err != nil {
		return err
	}
	defer amqpChan.Close()

	return amqpChan.Publish("", "builds.test", false, false, amqp.Publishing{
		Body: []byte(`{
			"type": "test",
			"job": {
				"id": 3,
				"number": "1.1",
				"commit": "abcdef",
				"commit_range": "abcde...abcdef",
				"commit_message": "Hello world",
				"branch": "master",
				"ref": null,
				"state": "queued",
				"secure_env_enabled": true,
				"pull_request": false
			},
			"source": {
				"id": 2,
				"number": "1"
			},
			"repository": {
				"id": 1,
				"slug": "hello/world",
				"github_id": 1234,
				"source_url": "git://github.com/hello/world.git",
				"api_url": "https://api.github.com",
				"last_build_id": 2,
				"last_build_number": "1",
				"last_build_started_at": null,
				"last_build_finished_at": null,
				"last_build_duration": null,
				"last_build_state": "created",
				"description": "Hello world",
				"config": {},
				"queue": "builds.test",
				"uuid": "fake-uuid",
				"ssh_key": null,
				"env_vars": [],
				"timeouts": {
					"hard_limit": null,
					"log_silence": null
				}
			}`),
		DeliveryMode: amqp.Persistent,
	})
}

func TestIntegrationLogMessages(t *testing.T) {
	if os.Getenv("AMQP_URI") == "" {
		t.Skip("Skipping integration test as AMQP_URI isn't set")
	}

	if os.Getenv("INTEGRATION_TESTS_DISABLED") != "" {
		t.Skip("Skipping disabled integration tests")
	}

	buildScriptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, world")
	}))
	defer buildScriptServer.Close()

	conn, err := setupConn()
	if err != nil {
		t.Fatal(err)
	}

	err = publishJob(conn)
	if err != nil {
		t.Fatal(err)
	}

	amqpChan, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}

	logParts := make([]LogPart, 2)

	delivery, _, err := amqpChan.Get("reporting.jobs.logs", true)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(delivery.Body, logParts[0])
	if err != nil {
		t.Fatal(err)
	}

	delivery, _, err = amqpChan.Get("reporting.jobs.logs", true)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(delivery.Body, logParts[1])
	if err != nil {
		t.Fatal(err)
	}

	sort.Sort(LogPartSlice(logParts))

	if logParts[0].ID != 3 {
		t.Errorf("logParts[0].ID = %d, expected 3", logParts[0].ID)
	}
	if !strings.Contains(logParts[0].Content, "Hello to the logs") {
		t.Errorf("logParts[0].Content = %q, expected to contain %q", logParts[0].Content, "Hello to the logs")
	}
	if logParts[0].Final {
		t.Errorf("logParts[0].Final = true, expected false")
	}
	if logParts[0].UUID != "fake-uuid" {
		t.Errorf("logParts[0].UUID = %q, expected fake-uuid", logParts[0].UUID)
	}

	expected := LogPart{ID: 3, Content: "", Final: true, UUID: "fake-uuid"}
	if logParts[1] != expected {
		t.Errorf("logParts[1] = %#v, expected %#v", logParts[1], expected)
	}
}
