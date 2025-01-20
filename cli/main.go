package main

import (
	"fmt"
	"os"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/cli/cmds/cluster"
	"github.com/rancher/k3k/cli/cmds/kubeconfig"
	"github.com/rancher/k3k/pkg/buildinfo"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func main() {
	app := cmds.NewApp()
	app.Version = buildinfo.Version
	cli.VersionPrinter = func(cCtx *cli.Context) {
		fmt.Println("k3kcli Version: " + buildinfo.Version)
	}

	app.Commands = []cli.Command{
		cluster.NewCommand(),
		kubeconfig.NewCommand(),
	}

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
