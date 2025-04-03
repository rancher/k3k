package cmds

import (
	"context"
	"errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ClusterSetCreateConfig struct {
	mode string
}

func NewClusterSetCreateCmd(appCtx *AppContext) *cli.Command {
	config := &ClusterSetCreateConfig{}

	createFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "mode",
			Usage:       "The allowed mode type of the clusterset",
			Destination: &config.mode,
			Value:       "shared",
			Action: func(ctx *cli.Context, value string) error {
				switch value {
				case string(v1alpha1.VirtualClusterMode), string(v1alpha1.SharedClusterMode):
					return nil
				default:
					return errors.New(`mode should be one of "shared" or "virtual"`)
				}
			},
		},
	}

	return &cli.Command{
		Name:            "create",
		Usage:           "Create new clusterset",
		UsageText:       "k3kcli clusterset create [command options] NAME",
		Action:          clusterSetCreateAction(appCtx, config),
		Flags:           append(CommonFlags, createFlags...),
		HideHelpCommand: true,
	}
}

func clusterSetCreateAction(appCtx *AppContext, config *ClusterSetCreateConfig) cli.ActionFunc {
	return func(clx *cli.Context) error {
		ctx := context.Background()
		client := appCtx.Client

		if clx.NArg() != 1 {
			return cli.ShowSubcommandHelp(clx)
		}

		name := clx.Args().First()
		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		namespace := Namespace(name)

		ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}

			logrus.Infof(`Creating namespace [%s]`, namespace)

			if err := client.Create(ctx, ns); err != nil {
				return err
			}
		}

		logrus.Infof("Creating clusterset [%s] in namespace [%s]", name, namespace)

		clusterSet := &v1alpha1.ClusterSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			TypeMeta: metav1.TypeMeta{
				Kind:       "ClusterSet",
				APIVersion: "k3k.io/v1alpha1",
			},
			Spec: v1alpha1.ClusterSetSpec{
				AllowedModeTypes: []v1alpha1.ClusterMode{v1alpha1.ClusterMode(config.mode)},
			},
		}

		if err := client.Create(ctx, clusterSet); err != nil {
			if apierrors.IsAlreadyExists(err) {
				logrus.Infof("ClusterSet [%s] already exists", name)
			} else {
				return err
			}
		}

		return nil
	}
}
