package main

import (
	"github.com/sirupsen/logrus"

	"github.com/rancher/k3k/cli/cmds"
)

func main() {
	app := cmds.NewRootCmd()
	if err := app.Execute(); err != nil {
		logrus.Fatal(err)
	}
}
