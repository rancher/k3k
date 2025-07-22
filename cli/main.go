package main

import (
	"os"

	"github.com/sirupsen/logrus"

	"github.com/rancher/k3k/cli/cmds"
)

func main() {
	app := cmds.NewApp()
	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
