package policy

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func (c *VirtualClusterPolicyReconciler) finalizePolicy(ctx context.Context, policy *v1beta1.VirtualClusterPolicy) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Deleting VirtualClusterPolicy")

	// Set Terminating condition
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "Terminating",
		Message: "Policy is being terminated",
	})

	// Update status to reflect terminating state
	if err := c.Client.Status().Update(ctx, policy); err != nil {
		log.Error(err, "failed to update policy status to Terminating")
		// Continue with cleanup even if status update fails
	}

	// Perform cleanup operations (best-effort, don't block on errors)
	if err := c.cleanupPolicyResources(ctx, policy); err != nil {
		log.Error(err, "failed to cleanup policy resources, continuing with finalization")
	}

	// Remove finalizer from the policy
	if controllerutil.RemoveFinalizer(policy, policyFinalizerName) {
		log.Info("Deleting VirtualClusterPolicy removing finalizer")

		if err := c.Client.Update(ctx, policy); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *VirtualClusterPolicyReconciler) cleanupPolicyResources(ctx context.Context, policy *v1beta1.VirtualClusterPolicy) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Cleaning up policy resources")

	// List all namespaces with this policy label
	listOpts := client.MatchingLabels{
		PolicyNameLabelKey: policy.Name,
	}

	var namespaces corev1.NamespaceList
	if err := c.Client.List(ctx, &namespaces, listOpts); err != nil {
		return err
	}

	var cleanupErrs []error

	// For each namespace bound to this policy
	for _, ns := range namespaces.Items {
		nsLog := log.WithValues("namespace", ns.Name)
		nsCtx := ctrl.LoggerInto(ctx, nsLog)

		nsLog.V(1).Info("Cleaning up namespace")

		// Clear policy fields from all clusters in this namespace
		if err := c.clearPolicyFieldsForClustersInNamespace(nsCtx, ns.Name); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("failed to clear cluster policy fields in namespace %s: %w", ns.Name, err))
			// Continue cleanup even if this fails
		}

		// Remove policy label and PSA labels from namespace
		orig := ns.DeepCopy()
		delete(ns.Labels, PolicyNameLabelKey)

		// Remove Pod Security Admission labels only if the policy set them
		if policy.Spec.PodSecurityAdmissionLevel != nil {
			delete(ns.Labels, "pod-security.kubernetes.io/enforce")
			delete(ns.Labels, "pod-security.kubernetes.io/enforce-version")
			delete(ns.Labels, "pod-security.kubernetes.io/warn")
			delete(ns.Labels, "pod-security.kubernetes.io/warn-version")
		}

		if !reflect.DeepEqual(orig.Labels, ns.Labels) {
			nsLog.V(1).Info("Updating namespace to remove policy labels")

			if err := c.Client.Update(ctx, &ns); err != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("failed to update namespace %s labels: %w", ns.Name, err))
				// Continue cleanup even if this fails
			}
		}
	}

	// Owned resources (NetworkPolicy, ResourceQuota, LimitRange) will be
	// automatically deleted by Kubernetes garbage collection via owner references

	return errors.Join(cleanupErrs...)
}
