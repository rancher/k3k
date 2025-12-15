package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/component-helpers/storage/volume"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	pvcControllerName = "pvc-syncer-controller"
	pvcFinalizerName  = "pvc.k3k.io/finalizer"
	pseudoPVLabel     = "pod.k3k.io/pseudoPV"
)

type PVCReconciler struct {
	*SyncerContext
}

// AddPVCSyncer adds persistentvolumeclaims syncer controller to k3k-kubelet
func AddPVCSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := PVCReconciler{
		SyncerContext: &SyncerContext{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			VirtualClient:    virtMgr.GetClient(),
			HostClient:       hostMgr.GetClient(),
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, pvcControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&v1.PersistentVolumeClaim{}).
		WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *PVCReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for pvc config
	syncConfig := cluster.Spec.Sync.PersistentVolumeClaims

	// If syncing is disabled, only process deletions to allow for cleanup.
	if !syncConfig.Enabled {
		return object.GetDeletionTimestamp() != nil
	}

	labelSelector := labels.SelectorFromSet(syncConfig.Selector)
	if labelSelector.Empty() {
		return true
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

func (r *PVCReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var (
		virtPVC v1.PersistentVolumeClaim
		cluster v1beta1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtPVC); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedPVC := r.pvc(&virtPVC)
	if err := controllerutil.SetControllerReference(&cluster, syncedPVC, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtPVC.DeletionTimestamp.IsZero() {
		// deleting the synced pvc if exists
		if err := r.HostClient.Delete(ctx, syncedPVC); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// delete the synced virtual PV
		if err := r.VirtualClient.Delete(ctx, newPersistentVolume(&virtPVC)); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced pvc
		if controllerutil.RemoveFinalizer(&virtPVC, pvcFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &virtPVC); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtPVC, pvcFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &virtPVC); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create the pvc on host
	log.Info("creating the persistent volume claim for the first time on the host cluster")

	// note that we dont need to update the PVC on the host cluster, only syncing the PVC to allow being
	// handled by the host cluster.
	if err := r.HostClient.Create(ctx, syncedPVC); err != nil && !apierrors.IsAlreadyExists(err) {
		return reconcile.Result{}, err
	}

	// Creating a virtual PV to bound the existing PVC in the virtual cluster - needed for scheduling of
	// the consumer pods
	return reconcile.Result{}, r.createVirtualPersistentVolume(ctx, virtPVC)
}

func (r *PVCReconciler) pvc(obj *v1.PersistentVolumeClaim) *v1.PersistentVolumeClaim {
	hostPVC := obj.DeepCopy()
	r.Translator.TranslateTo(hostPVC)

	return hostPVC
}

func (r *PVCReconciler) createVirtualPersistentVolume(ctx context.Context, pvc v1.PersistentVolumeClaim) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Creating virtual PersistentVolume")

	pv := newPersistentVolume(&pvc)

	if err := r.VirtualClient.Create(ctx, pv); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	orig := pv.DeepCopy()
	pv.Status = v1.PersistentVolumeStatus{
		Phase: v1.VolumeBound,
	}

	if err := r.VirtualClient.Status().Patch(ctx, pv, ctrlruntimeclient.MergeFrom(orig)); err != nil {
		return err
	}

	log.V(1).Info("Patch the status of PersistentVolumeClaim to Bound")

	pvcPatch := pvc.DeepCopy()
	if pvcPatch.Annotations == nil {
		pvcPatch.Annotations = make(map[string]string)
	}

	pvcPatch.Annotations[volume.AnnBoundByController] = "yes"
	pvcPatch.Annotations[volume.AnnBindCompleted] = "yes"
	pvcPatch.Status.Phase = v1.ClaimBound
	pvcPatch.Status.AccessModes = pvcPatch.Spec.AccessModes

	return r.VirtualClient.Status().Update(ctx, pvcPatch)
}

func newPersistentVolume(obj *v1.PersistentVolumeClaim) *v1.PersistentVolume {
	var storageClass string

	if obj.Spec.StorageClassName != nil {
		storageClass = *obj.Spec.StorageClassName
	}

	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: obj.Name,
			Labels: map[string]string{
				pseudoPVLabel: "true",
			},
			Annotations: map[string]string{
				volume.AnnBoundByController:      "true",
				volume.AnnDynamicallyProvisioned: "k3k-kubelet",
			},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolume",
			APIVersion: "v1",
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeSource: v1.PersistentVolumeSource{
				FlexVolume: &v1.FlexPersistentVolumeSource{
					Driver: "pseudopv",
				},
			},
			StorageClassName:              storageClass,
			VolumeMode:                    obj.Spec.VolumeMode,
			PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimDelete,
			AccessModes:                   obj.Spec.AccessModes,
			Capacity:                      obj.Spec.Resources.Requests,
			ClaimRef: &v1.ObjectReference{
				APIVersion:      obj.APIVersion,
				UID:             obj.UID,
				ResourceVersion: obj.ResourceVersion,
				Kind:            obj.Kind,
				Namespace:       obj.Namespace,
				Name:            obj.Name,
			},
		},
	}
}
