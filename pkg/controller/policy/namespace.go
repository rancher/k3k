package policy

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		currentPolicyName := ns.Labels[PolicyNameLabelKey]

		// If the namespace is not bound to any policy, or if the policy it was bound to no longer exists,
		// we need to clear policy-related fields on its Cluster objects.
		if currentPolicyName == "" {
			if err := c.clearPolicyFieldsForClustersInNamespace(ctx, ns.Name); err != nil {
				log.Error(err, "error clearing policy fields for clusters in unbound namespace", "namespace", ns.Name)
			}
		} else {
			var policy v1beta1.VirtualClusterPolicy
			if err := c.Client.Get(ctx, types.NamespacedName{Name: currentPolicyName}, &policy); apierrors.IsNotFound(err) {
				if err := c.clearPolicyFieldsForClustersInNamespace(ctx, ns.Name); err != nil {
					log.Error(err, "error clearing policy fields for clusters in namespace with non-existent policy", "namespace", ns.Name, "policy", currentPolicyName)
				}
			} else if err != nil {
				log.Error(err, "error getting policy for namespace", "namespace", ns.Name, "policy", currentPolicyName)
			}
		}

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

// clearPolicyFieldsForClustersInNamespace sets the policy status on Cluster objects in the given namespace to nil.
func (c *VirtualClusterPolicyReconciler) clearPolicyFieldsForClustersInNamespace(ctx context.Context, namespace string) error {
	log := ctrl.LoggerFrom(ctx)

	var clusters v1beta1.ClusterList
	if err := c.Client.List(ctx, &clusters, client.InNamespace(namespace)); err != nil {
		return err
	}

	var updateErrs []error

	for i := range clusters.Items {
		cluster := clusters.Items[i]
		// Only update if there are policy fields to clear to avoid unnecessary reconciliation loops.
		if cluster.Status.Policy != nil {
			log.V(1).Info("Clearing policy status for Cluster", "cluster", cluster.Name, "namespace", namespace)
			cluster.Status.Policy = nil

			// Use Status().Update() to avoid race conditions and honor the separation
			// between spec and status.
			if err := c.Client.Status().Update(ctx, &cluster); err != nil {
				updateErrs = append(updateErrs, err)
			}
		}
	}

	return errors.Join(updateErrs...)
}
