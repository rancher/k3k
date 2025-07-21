package cmds

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/policy"
)

type VirtualClusterPolicyCreateConfig struct {
	mode string
}

func NewPolicyCreateCmd(appCtx *AppContext) *cobra.Command {
	config := &VirtualClusterPolicyCreateConfig{}

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create new policy",
		Example: "k3kcli policy create [command options] NAME",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			switch config.mode {
			case string(v1alpha1.VirtualClusterMode), string(v1alpha1.SharedClusterMode):
				return nil
			default:
				return errors.New(`mode should be one of "shared" or "virtual"`)
			}
		},
		RunE: policyCreateAction(appCtx, config),
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVar(&config.mode, "mode", "shared", "The allowed mode type of the policy")

	return cmd
}

func policyCreateAction(appCtx *AppContext, config *VirtualClusterPolicyCreateConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client
		policyName := args[0]

		_, err := createPolicy(ctx, client, v1alpha1.ClusterMode(config.mode), policyName)

		return err
	}
}

func createNamespace(ctx context.Context, client client.Client, name, policyName string) error {
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}

	if policyName != "" {
		ns.Labels = map[string]string{
			policy.PolicyNameLabelKey: policyName,
		}
	}

	if err := client.Get(ctx, types.NamespacedName{Name: name}, ns); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		logrus.Infof(`Creating namespace [%s]`, name)

		if err := client.Create(ctx, ns); err != nil {
			return err
		}
	}

	return nil
}

func createPolicy(ctx context.Context, client client.Client, mode v1alpha1.ClusterMode, policyName string) (*v1alpha1.VirtualClusterPolicy, error) {
	logrus.Infof("Creating policy [%s]", policyName)

	policy := &v1alpha1.VirtualClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: policyName,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "VirtualClusterPolicy",
			APIVersion: "k3k.io/v1alpha1",
		},
		Spec: v1alpha1.VirtualClusterPolicySpec{
			AllowedMode: mode,
		},
	}

	if err := client.Create(ctx, policy); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}

		logrus.Infof("Policy [%s] already exists", policyName)
	}

	return policy, nil
}
