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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VirtualClusterPolicyCreateConfig struct {
	mode        string
	displayName string
}

func NewPolicyCreateCmd(appCtx *AppContext) *cli.Command {
	config := &VirtualClusterPolicyCreateConfig{}

	createFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "mode",
			Usage:       "The allowed mode type of the policy",
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
		&cli.StringFlag{
			Name:        "display-name",
			Usage:       "The display name of the policy",
			Destination: &config.displayName,
		},
	}

	return &cli.Command{
		Name:            "create",
		Usage:           "Create new policy",
		UsageText:       "k3kcli policy create [command options] NAME",
		Action:          policyCreateAction(appCtx, config),
		Flags:           WithCommonFlags(appCtx, createFlags...),
		HideHelpCommand: true,
	}
}

func policyCreateAction(appCtx *AppContext, config *VirtualClusterPolicyCreateConfig) cli.ActionFunc {
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

		displayName := config.displayName
		if displayName == "" {
			displayName = name
		}

		// if both display name and namespace are set the name is ignored
		if config.displayName != "" && appCtx.namespace != "" {
			logrus.Warnf("Ignoring name [%s] because display name and namespace are set", name)
		}

		namespace := appCtx.Namespace(name)

		if err := createNamespace(ctx, client, namespace); err != nil {
			return err
		}

		_, err := createPolicy(ctx, client, namespace, v1alpha1.ClusterMode(config.mode), displayName)

		return err
	}
}

func createNamespace(ctx context.Context, client client.Client, name string) error {
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
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

func createPolicy(ctx context.Context, client client.Client, namespace string, mode v1alpha1.ClusterMode, displayName string) (*v1alpha1.VirtualClusterPolicy, error) {
	logrus.Infof("Creating policy in namespace [%s]", namespace)

	policy := &v1alpha1.VirtualClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "VirtualClusterPolicy",
			APIVersion: "k3k.io/v1alpha1",
		},
		Spec: v1alpha1.VirtualClusterPolicySpec{
			AllowedModeTypes: []v1alpha1.ClusterMode{mode},
			DisplayName:      displayName,
		},
	}

	if err := client.Create(ctx, policy); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logrus.Infof("Policy in namespace [%s] already exists", namespace)
		} else {
			return nil, err
		}
	}

	return policy, nil
}
