package cmds

import (
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	defaultNamespace = "default"
)

var (
	debug       bool
	Kubeconfig  string
	namespace   string
	CommonFlags = []cli.Flag{
		cli.StringFlag{
			Name:        "kubeconfig",
			EnvVar:      "KUBECONFIG",
			Usage:       "Kubeconfig path",
			Destination: &Kubeconfig,
		},
		cli.StringFlag{
			Name:        "namespace",
			Usage:       "Namespace to create the k3k cluster in",
			Destination: &namespace,
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

func Namespace() string {
	if namespace == "" {
		return defaultNamespace
	}
	return namespace
}
