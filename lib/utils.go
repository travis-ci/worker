package lib

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"regexp"
	"time"
)

var (
	punctRegex = regexp.MustCompile(`[&+/=\\]`)
)

func waitFor(done func() bool, interval time.Duration) (doneChan chan bool, cancelChan chan bool) {
	doneChan = make(chan bool)
	cancelChan = make(chan bool)

	go func() {
		for !done() {
			time.Sleep(interval)
			select {
			case <-cancelChan:
				break
			default:
			}
		}

		doneChan <- true
		close(doneChan)
		close(cancelChan)
	}()

	return
}

func generatePassword() string {
	randomBytes := make([]byte, 30)
	rand.Read(randomBytes)
	hash := sha1.New().Sum(randomBytes)
	str := base64.StdEncoding.EncodeToString(hash)
	return punctRegex.ReplaceAllLiteralString(str, "")[0:19]
}
