package kubeconfig

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
	"github.com/rancher/k3k/pkg/controller/util"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authentication/user"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

var (
	Scheme         = runtime.NewScheme()
	name           string
	cn             string
	org            cli.StringSlice
	altNames       cli.StringSlice
	expirationDays int64
	configName     string
	backoff        = wait.Backoff{
		Steps:    5,
		Duration: 20 * time.Second,
		Factor:   2,
		Jitter:   0.1,
	}
	generateKubeconfigFlags = []cli.Flag{
		cli.StringFlag{
			Name:        "name",
			Usage:       "cluster name",
			Destination: &name,
		},
		cli.StringFlag{
			Name:        "config-name",
			Usage:       "the name of the generated kubeconfig file",
			Destination: &configName,
		},
		cli.StringFlag{
			Name:        "cn",
			Usage:       "Common name (CN) of the generated certificates for the kubeconfig",
			Destination: &cn,
			Value:       util.AdminCommonName,
		},
		cli.StringSliceFlag{
			Name:  "org",
			Usage: "Organization name (ORG) of the generated certificates for the kubeconfig",
			Value: &org,
		},
		cli.StringSliceFlag{
			Name:  "altNames",
			Usage: "altNames of the generated certificates for the kubeconfig",
			Value: &altNames,
		},
		cli.Int64Flag{
			Name:        "expiration-days",
			Usage:       "Expiration date of the certificates used for the kubeconfig",
			Destination: &expirationDays,
			Value:       356,
		},
	}
)

var subcommands = []cli.Command{
	{
		Name:            "generate",
		Usage:           "Generate kubeconfig for clusters",
		SkipFlagParsing: false,
		SkipArgReorder:  true,
		Action:          generate,
		Flags:           append(cmds.CommonFlags, generateKubeconfigFlags...),
	},
}

func NewCommand() cli.Command {
	return cli.Command{
		Name:        "kubeconfig",
		Usage:       "Manage kubeconfig for clusters",
		Subcommands: subcommands,
	}
}

func generate(clx *cli.Context) error {
	var cluster v1alpha1.Cluster
	ctx := context.Background()

	restConfig, err := clientcmd.BuildConfigFromFlags("", cmds.Kubeconfig)
	if err != nil {
		return err
	}

	ctrlClient, err := client.New(restConfig, client.Options{
		Scheme: Scheme,
	})
	if err != nil {
		return err
	}

	clusterKey := types.NamespacedName{
		Name: name,
	}

	if err := ctrlClient.Get(ctx, clusterKey, &cluster); err != nil {
		return err
	}

	url, err := url.Parse(restConfig.Host)
	if err != nil {
		return err
	}
	host := strings.Split(url.Host, ":")

	certAltNames := kubeconfig.AddSANs(altNames)
	if org == nil {
		org = cli.StringSlice{user.SystemPrivilegedGroup}
	}
	cfg := kubeconfig.KubeConfig{
		CN:         cn,
		ORG:        org,
		ExpiryDate: time.Hour * 24 * time.Duration(expirationDays),
		AltNames:   certAltNames,
	}
	logrus.Infof("waiting for cluster to be available..")
	var kubeconfig []byte
	if err := retry.OnError(backoff, apierrors.IsNotFound, func() error {
		kubeconfig, err = cfg.Extract(ctx, ctrlClient, &cluster, host[0])
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if configName == "" {
		configName = cluster.Name + "-kubeconfig.yaml"
	}

	logrus.Infof(`You can start using the cluster with: 

	export KUBECONFIG=%s
	kubectl cluster-info
	`, filepath.Join(pwd, configName))

	return os.WriteFile(configName, kubeconfig, 0644)
}
