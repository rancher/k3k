package cmds

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

func NewPolicyDeleteCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:            "delete",
		Usage:           "Delete an existing policy",
		UsageText:       "k3kcli policy delete [command options] NAME",
		Action:          policyDeleteAction(appCtx),
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

		policy := &v1alpha1.VirtualClusterPolicy{}
		policy.Name = name

		if err := client.Delete(ctx, policy); err != nil {
			if apierrors.IsNotFound(err) {
				logrus.Warnf("Policy not found")
			} else {
				return err
			}
		}

		return nil
	}
}
