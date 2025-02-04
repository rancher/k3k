package cmds

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

const (
	defaultNamespace = "default"
)

var (
	debug       bool
	Kubeconfig  string
	namespace   string
	CommonFlags = []cli.Flag{
		&cli.StringFlag{
			Name:        "kubeconfig",
			EnvVars:     []string{"KUBECONFIG"},
			Usage:       "kubeconfig path",
			Destination: &Kubeconfig,
			Value:       os.Getenv("HOME") + "/.kube/config",
		},
		&cli.StringFlag{
			Name:        "namespace",
			Usage:       "namespace to create the k3k cluster in",
			Destination: &namespace,
		},
	}
)

func NewApp() *cli.App {
	app := cli.NewApp()
	app.Name = "k3kcli"
	app.Usage = "CLI for K3K"
	app.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:        "debug",
			Usage:       "Turn on debug logs",
			Destination: &debug,
			EnvVars:     []string{"K3K_DEBUG"},
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
