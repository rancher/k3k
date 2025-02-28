package main

import (
	"os"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/sirupsen/logrus"
)

func main() {
	app := cmds.NewApp()
	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
