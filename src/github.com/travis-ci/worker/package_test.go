package worker

import "github.com/Sirupsen/logrus"

func init() {
	logrus.SetLevel(logrus.FatalLevel)
}
