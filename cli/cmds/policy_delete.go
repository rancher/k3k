package cmds

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

func NewPolicyDeleteCmd(appCtx *AppContext) *cobra.Command {
	return &cobra.Command{
		Use:     "delete",
		Short:   "Delete an existing policy",
		Example: "k3kcli policy delete [command options] NAME",
		RunE:    policyDeleteAction(appCtx),
		Args:    cobra.ExactArgs(1),
	}
}

func policyDeleteAction(appCtx *AppContext) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client
		name := args[0]

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
