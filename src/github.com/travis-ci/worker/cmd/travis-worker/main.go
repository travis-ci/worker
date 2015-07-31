package main

import (
	"os"

	"github.com/codegangsta/cli"
	"github.com/travis-ci/worker"
	"github.com/travis-ci/worker/config"
)

func main() {
	app := cli.NewApp()
	app.Usage = "Travis Worker daemon"
	app.Version = worker.VersionString
	app.Author = "Travis CI GmbH"
	app.Email = "contact+travis-worker@travis-ci.com"
	app.Copyright = worker.CopyrightString

	app.Flags = config.Flags
	app.Action = runWorker

	app.Run(os.Args)
}

func runWorker(c *cli.Context) {
	workerCLI := worker.NewCLI(c)
	successfullySetup := workerCLI.Setup()
	if !successfullySetup {
		return
	}
	workerCLI.Run()
}
