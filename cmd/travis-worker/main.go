package main

import (
	"fmt"
	"os"

	"github.com/codegangsta/cli"
	"github.com/travis-ci/worker"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
)

const (
	exitAlarm = 14
)

func main() {
	app := cli.NewApp()
	app.Usage = "Travis Worker"
	app.Version = worker.VersionString
	app.Author = "Travis CI GmbH"
	app.Email = "contact+travis-worker@travis-ci.com"
	app.Copyright = worker.CopyrightString
	app.Action = runWorker
	app.Commands = []cli.Command{
		{
			Name:        "work",
			Aliases:     []string{"w"},
			Usage:       "worky worky work work",
			Subcommands: workWithQueueTypesCommands(),
		},
		{
			Name:    "echo-config",
			Aliases: []string{"e"},
			Usage:   "echo the parsed config as env vars",
			Action:  echoConfig,
		},
		{
			Name:    "list-backend-providers",
			Aliases: []string{"lp"},
			Usage:   "list available backend providers",
			Action:  listBackendProviders,
		},
		{
			Name:    "list-queue-types",
			Aliases: []string{"lq"},
			Usage:   "list available queue types",
			Action:  listQueueTypes,
		},
	}

	app.Run(os.Args)
}

func runWorker(c *cli.Context) {
	if config.DefaultConfig.ProviderName == "" {
		config.DefaultConfig.ProviderName = "docker"
	}

	if config.DefaultConfig.QueueType == "" {
		config.DefaultConfig.QueueType = "amqp"
	}

	workerCLI := worker.NewCLI(c)
	canRun, err := workerCLI.Setup()
	if !canRun {
		if err != nil {
			os.Exit(exitAlarm)
		}
		return
	}
	workerCLI.Run()
}

func echoConfig(c *cli.Context) {
	workerCLI := worker.NewCLI(c)
	workerCLI.Configure()
	config.WriteEnvConfig(workerCLI.Config, os.Stdout)
}

func listBackendProviders(c *cli.Context) {
	backend.EachBackend(func(b *backend.Backend) {
		fmt.Println(b.Alias)
	})
}

func listQueueTypes(c *cli.Context) {
	for _, t := range config.QueueTypes {
		fmt.Println(t)
	}
}

func workWithQueueTypesCommands() []cli.Command {
	commands := []cli.Command{}

	for _, t := range config.QueueTypes {
		flags := []cli.Flag{}

		if t == "amqp" {
			flags = config.WorkAMQPFlags
		} else if t == "file" {
			flags = config.WorkFileFlags
		}

		commands = append(commands, cli.Command{
			Name:        t,
			Aliases:     []string{string(t[0])},
			Usage:       fmt.Sprintf("Work with queue type of %q", t),
			Subcommands: workWithBackendProviderCommands(t, flags),
		})
	}

	return commands
}

func workWithBackendProviderCommands(queueType string, flags []cli.Flag) []cli.Command {
	config.DefaultConfig.QueueType = queueType
	commands := []cli.Command{}

	backend.EachBackend(func(b *backend.Backend) {
		commands = append(commands, cli.Command{
			Name:    b.Alias,
			Aliases: []string{string(b.Alias[0])},
			Usage:   fmt.Sprintf("Work with queue type of %q and backend of %q", queueType, b.Alias),
			Flags:   append(b.Flags, flags...),
			Action: func(c *cli.Context) {
				config.DefaultConfig.ProviderName = b.Alias
				runWorker(c)
			},
		})
	})

	return commands
}
