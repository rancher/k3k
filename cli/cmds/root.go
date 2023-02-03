package cmds

import (
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var (
	debug       bool
	Kubeconfig  string
	CommonFlags = []cli.Flag{
		cli.StringFlag{
			Name:        "kubeconfig",
			EnvVar:      "KUBECONFIG",
			Usage:       "Kubeconfig path",
			Destination: &Kubeconfig,
		},
	}
)

func NewApp() *cli.App {
	app := cli.NewApp()
	app.Name = "k3kcli"
	app.Usage = "CLI for K3K"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:        "debug",
			Usage:       "Turn on debug logs",
			Destination: &debug,
			EnvVar:      "K3K_DEBUG",
		},
	}

	app.Before = func(clx *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}

	return app
}
