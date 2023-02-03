package main

import (
	"os"

	"github.com/galal-hussein/k3k/cli/cmds"
	"github.com/galal-hussein/k3k/cli/cmds/cluster"
	"github.com/galal-hussein/k3k/pkg/version"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func main() {
	app := cmds.NewApp()
	app.Commands = []cli.Command{
		cluster.NewClusterCommand(),
	}
	app.Version = version.Version + " (" + version.GitCommit + ")"

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
