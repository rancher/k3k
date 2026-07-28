package cmds

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

var completeClusterMode = cobra.FixedCompletions(
	[]string{
		string(v1beta1.SharedClusterMode),
		string(v1beta1.VirtualClusterMode),
	},
	cobra.ShellCompDirectiveNoFileComp,
)

var completePersistenceMode = cobra.FixedCompletions(
	[]string{
		string(v1beta1.DynamicPersistenceMode),
		string(v1beta1.EphemeralPersistenceMode),
	},
	cobra.ShellCompDirectiveNoFileComp,
)

// completeNamespaces is a cobra.CompletionFunc that completes with every namespace in the host cluster.
func completeNamespaces(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := completionClient(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return namespaceCompletions(cmd, client)
}

// completeClusterNamespaces is a cobra.CompletionFunc that completes with the namespaces with a k3k virtual cluster in it.
func completeClusterNamespaces(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := completionClient(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return clusterNamespaceCompletions(cmd.Context(), client)
}

// completeClusterNames is a cobra.CompletionFunc that completes the cluster name argument with "namespace/name" values.
// When the "namespace" flag is set the results are filtered to that namespace.
func completeClusterNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// only the first positional argument is a cluster name
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// with --all no cluster name is expected, so suppress completions
	if all, _ := cmd.Flags().GetBool("all"); all {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	cl, err := completionClient(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	namespace, _ := cmd.Flags().GetString("namespace")

	return clusterNameCompletions(cmd.Context(), cl, namespace)
}

// namespaceCompletions lists every namespace in the host cluster, excluding any
// already provided to the command's "namespace" flag.
func namespaceCompletions(cmd *cobra.Command, cl client.Client) ([]string, cobra.ShellCompDirective) {
	var namespaces corev1.NamespaceList
	if err := cl.List(cmd.Context(), &namespaces); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	allNames := sets.New[string]()
	for _, ns := range namespaces.Items {
		allNames.Insert(ns.Name)
	}

	// Exclude already selected namespaces
	selected := sets.New[string]()
	if vals, err := cmd.Flags().GetStringSlice("namespace"); err == nil {
		selected.Insert(vals...)
	}

	names := allNames.Difference(selected)

	return sets.List(names), cobra.ShellCompDirectiveNoFileComp
}

// clusterNamespaceCompletions lists the unique namespaces that contain at least one k3k Cluster.
func clusterNamespaceCompletions(ctx context.Context, client client.Client) ([]string, cobra.ShellCompDirective) {
	var clusters v1beta1.ClusterList
	if err := client.List(ctx, &clusters); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	names := sets.New[string]()
	for _, cluster := range clusters.Items {
		names.Insert(cluster.Namespace)
	}

	return sets.List(names), cobra.ShellCompDirectiveNoFileComp
}

// clusterNameCompletions lists the k3k Clusters as "namespace/name" values, optionally filtered to a single namespace.
func clusterNameCompletions(ctx context.Context, cl client.Client, namespace string) ([]string, cobra.ShellCompDirective) {
	var opts []client.ListOption

	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	var clusters v1beta1.ClusterList
	if err := cl.List(ctx, &clusters, opts...); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	names := make([]string, 0, len(clusters.Items))
	for _, cluster := range clusters.Items {
		names = append(names, cluster.Namespace+"/"+cluster.Name)
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// mustRegisterFlagCompletion registers a completion function for a flag and
// aborts if the flag does not exist. This only fails on programmer error, so
// there is no reason to bubble it up to the caller.
func mustRegisterFlagCompletion(cmd *cobra.Command, flagName string, f cobra.CompletionFunc) {
	if err := cmd.RegisterFlagCompletionFunc(flagName, f); err != nil {
		logrus.Fatal(err)
	}
}

// disableFileCompletion walks the command tree and turns off cobra's default
// filename completion for both positional arguments and flag values, leaving
// anything that is explicitly configured (enum completers, MarkFlagFilename,
// MarkFlagDirname, ValidArgs, bool flags) untouched.
func disableFileCompletion(cmd *cobra.Command) {
	if cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0 {
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Value.Type() == "bool" {
			return
		}

		if _, ok := cmd.GetFlagCompletionFunc(f.Name); ok {
			return
		}

		if _, ok := f.Annotations[cobra.BashCompFilenameExt]; ok {
			return
		}

		if _, ok := f.Annotations[cobra.BashCompSubdirsInDir]; ok {
			return
		}

		_ = cmd.RegisterFlagCompletionFunc(f.Name, cobra.NoFileCompletions)
	})

	for _, sub := range cmd.Commands() {
		disableFileCompletion(sub)
	}
}

// completionClient builds a Kubernetes client for use inside completion functions.
// It checks if the kubeconfig flag is set, to point to the right cluster.
func completionClient(cmd *cobra.Command) (client.Client, error) {
	kubeconfig, err := cmd.Flags().GetString("kubeconfig")
	if err != nil {
		return nil, err
	}

	restConfig, err := loadRESTConfig(kubeconfig, completionRequestTimeout)
	if err != nil {
		return nil, err
	}

	client, err := buildClient(restConfig)
	if err != nil {
		return nil, err
	}

	return client, nil
}
