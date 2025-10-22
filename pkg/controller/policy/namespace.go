package policy

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// reconcileNamespacePodSecurityLabels will update the labels of the namespace to reconcile the PSA level specified in the VirtualClusterPolicy
func (c *VirtualClusterPolicyReconciler) reconcileNamespacePodSecurityLabels(ctx context.Context, namespace *v1.Namespace, policy *v1beta1.VirtualClusterPolicy) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Reconciling PSA labels")

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
		if psaLevel != v1beta1.PrivilegedPodSecurityAdmissionLevel {
			namespace.Labels["pod-security.kubernetes.io/warn"] = string(psaLevel)
			namespace.Labels["pod-security.kubernetes.io/warn-version"] = "latest"
		}
	}
}

// cleanupNamespaces will cleanup the Namespaces without the "policy.k3k.io/policy-name" label
// deleting the resources in them with the "app.kubernetes.io/managed-by=k3k-policy-controller" label
func (c *VirtualClusterPolicyReconciler) cleanupNamespaces(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Cleanup Namespace resources")

	var namespaces v1.NamespaceList
	if err := c.Client.List(ctx, &namespaces); err != nil {
		return err
	}

	for _, ns := range namespaces.Items {
		selector := labels.NewSelector()

		if req, err := labels.NewRequirement(ManagedByLabelKey, selection.Equals, []string{VirtualPolicyControllerName}); err == nil {
			selector = selector.Add(*req)
		}

		// if the namespace is bound to a policy -> cleanup resources of other policies
		if ns.Labels[PolicyNameLabelKey] != "" {
			requirement, err := labels.NewRequirement(PolicyNameLabelKey, selection.NotEquals, []string{ns.Labels[PolicyNameLabelKey]})

			// log the error but continue cleaning up the other namespaces
			if err != nil {
				log.Error(err, "error creating requirement", "policy", ns.Labels[PolicyNameLabelKey])
			} else {
				selector = selector.Add(*requirement)
			}
		}

		deleteOpts := []client.DeleteAllOfOption{
			client.InNamespace(ns.Name),
			client.MatchingLabelsSelector{Selector: selector},
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
