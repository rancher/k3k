package policy

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *VirtualClusterPolicyReconciler) reconcileNamespacePodSecurityLabels(ctx context.Context, namespace *v1.Namespace, policy *v1alpha1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling Namespace")

	// cleanup of old labels
	delete(namespace.Labels, "pod-security.kubernetes.io/enforce")
	delete(namespace.Labels, "pod-security.kubernetes.io/enforce-version")
	delete(namespace.Labels, "pod-security.kubernetes.io/warn")
	delete(namespace.Labels, "pod-security.kubernetes.io/warn-version")

	// if a PSA level is specified add the proper labels
	if policy.Spec.PodSecurityAdmissionLevel != nil {
		psaLevel := *policy.Spec.PodSecurityAdmissionLevel

		namespace.Labels["pod-security.kubernetes.io/enforce"] = string(psaLevel)
		namespace.Labels["pod-security.kubernetes.io/enforce-version"] = "latest"

		// skip the 'warn' only for the privileged PSA level
		if psaLevel != v1alpha1.PrivilegedPodSecurityAdmissionLevel {
			namespace.Labels["pod-security.kubernetes.io/warn"] = string(psaLevel)
			namespace.Labels["pod-security.kubernetes.io/warn-version"] = "latest"
		}
	}

	return nil
}

func (c *VirtualClusterPolicyReconciler) cleanupNamespaces(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("deleting resources")

	requirement, err := labels.NewRequirement(PolicyNameLabelKey, selection.DoesNotExist, nil)
	if err != nil {
		return err
	}

	sel := labels.NewSelector().Add(*requirement)

	var namespaces v1.NamespaceList
	if err := c.Client.List(ctx, &namespaces, client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return err
	}

	for _, ns := range namespaces.Items {
		deleteOpts := []client.DeleteAllOfOption{
			client.InNamespace(ns.Name),
			client.MatchingLabels{ManagedByLabelKey: VirtualPolicyControllerName},
		}

		if err := c.Client.DeleteAllOf(ctx, &networkingv1.NetworkPolicy{}, deleteOpts...); err != nil {
			return err
		}

		if err := c.Client.DeleteAllOf(ctx, &v1.ResourceQuota{}, deleteOpts...); err != nil {
			return err
		}

		if err := c.Client.DeleteAllOf(ctx, &v1.LimitRange{}, deleteOpts...); err != nil {
			return err
		}
	}

	return nil
}
