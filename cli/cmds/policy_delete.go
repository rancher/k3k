package cmds

import (
	"context"
	"errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewPolicyDeleteCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:            "delete",
		Usage:           "Delete an existing policy",
		UsageText:       "k3kcli policy delete [command options] NAME",
		Action:          policyDeleteAction(appCtx),
		Flags:           WithCommonFlags(appCtx),
		HideHelpCommand: true,
	}
}

func policyDeleteAction(appCtx *AppContext) cli.ActionFunc {
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

		namespace := appCtx.Namespace(name)

		logrus.Infof("Deleting policy in namespace [%s]", namespace)

		policy := &v1alpha1.VirtualClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: namespace,
			},
		}

		if err := client.Delete(ctx, policy); err != nil {
			if apierrors.IsNotFound(err) {
				logrus.Warnf("Policy not found in namespace [%s]", namespace)
			} else {
				return err
			}
		}

		return nil
	}
}
