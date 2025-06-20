package cmds

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func NewPolicyDeleteCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:            "delete",
		Usage:           "Delete an existing policy",
		UsageText:       "k3kcli policy delete [command options] NAME",
		Action:          policyDeleteAction(appCtx),
		Flags:           CommonFlags(appCtx),
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
