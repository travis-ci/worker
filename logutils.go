package main

import (
	"fmt"
	"io"
	"path"
	"runtime"
	"strings"
	"time"
)

// A Logger contains the information required to log messages to an io.Writer.
// It uses an l2met-like format of key=value pairs that can be set.
//
// An example of a log message might be:
//
//    name=worker-1a pid=589 slug=travis-ci/travis-ci job_id=123 level=info source=ssh message="hello world"
//
// To generate this line, you could do the following:
//
//   NewLogger(os.Stdout).Set("name", "worker-1a").Set("pid", "589").Set("slug", "travis-ci/travis-ci").Set("job_id", "123").Info("hello world")
//
// The source field is automatically extracted from the filename of the file
// where the log call was made from.
type Logger struct {
	io.Writer
	timestampFormat string
	prefix          string
}

func NewLogger(w io.Writer, timestampFormat string) *Logger {
	return &Logger{w, timestampFormat, ""}
}

// Set adds a new key=value pair to the output of the returned Logger
func (l *Logger) Set(key string, value interface{}) *Logger {
	newPrefix := l.prefix + " " + makeKeyValue(key, fmt.Sprintf("%v", value))
	if len(l.prefix) == 0 {
		newPrefix = newPrefix[1:]
	}

	return &Logger{l.Writer, l.timestampFormat, newPrefix}
}

func (l *Logger) Info(message string) error {
	return l.writeLevel("info", message)
}

func (l *Logger) Infof(format string, a ...interface{}) error {
	return l.writeLevel("info", fmt.Sprintf(format, a...))
}

func (l *Logger) Error(message string) error {
	return l.writeLevel("error", message)
}

func (l *Logger) Errorf(format string, a ...interface{}) error {
	return l.writeLevel("error", fmt.Sprintf(format, a...))
}

func (l *Logger) Warn(message string) error {
	return l.writeLevel("warn", message)
}

func (l *Logger) Warnf(format string, a ...interface{}) error {
	return l.writeLevel("warn", fmt.Sprintf(format, a...))
}

func (l *Logger) writeLevel(level, message string) error {
	_, fileName, _, _ := runtime.Caller(2)
	return l.Set("level", level).Set("source", strings.TrimSuffix(path.Base(fileName), ".go")).Set("message", message).flush()
}

func (l *Logger) flush() error {
	_, err := l.Write([]byte(time.Now().Format(l.timestampFormat) + l.prefix + "\n"))
	return err
}

func makeKeyValue(key, value string) string {
	if strings.ContainsRune(value, ' ') {
		value = "\"" + value + "\""
	}

	return key + "=" + value
}
