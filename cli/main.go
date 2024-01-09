package main

import (
	"os"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/cli/cmds/cluster"
	"github.com/rancher/k3k/cli/cmds/kubeconfig"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	program   = "k3k"
	version   = "dev"
	gitCommit = "HEAD"
)

func main() {
	app := cmds.NewApp()
	app.Commands = []cli.Command{
		cluster.NewClusterCommand(),
		kubeconfig.NewKubeconfigCommand(),
	}
	app.Version = version + " (" + gitCommit + ")"

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
