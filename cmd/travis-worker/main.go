// Package main implements the CLI for the travis-worker binary
package main

import (
	"os"

	"github.com/travis-ci/worker"
	"github.com/travis-ci/worker/config"
	"gopkg.in/urfave/cli.v1"
)

func main() {
	app := cli.NewApp()
	app.Usage = "Travis Worker"
	app.Version = worker.VersionString
	app.Author = "Travis CI GmbH"
	app.Email = "contact+travis-worker@travis-ci.com"
	app.Copyright = worker.CopyrightString

	app.Flags = config.Flags
	app.Action = runWorker

	_ = app.Run(os.Args)
}

func runWorker(c *cli.Context) error {
	workerCLI := worker.NewCLI(c)
	canRun, err := workerCLI.Setup()
	if err != nil {
		return err
	}
	if canRun {
		workerCLI.Run()
	}
	return nil
}
