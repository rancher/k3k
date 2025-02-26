package controller

import (
	"context"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/component-helpers/storage/volume"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	podController = "pod-pvc-controller"
	pseudoPVLabel = "pod.k3k.io/pseudoPV"
)

type PodReconciler struct {
	virtualClient    ctrlruntimeclient.Client
	hostClient       ctrlruntimeclient.Client
	clusterName      string
	clusterNamespace string
	Scheme           *runtime.Scheme
	HostScheme       *runtime.Scheme
	logger           *log.Logger
	Translator       translate.ToHostTranslator
}

// AddPodPVCController adds pod controller to k3k-kubelet
func AddPodPVCController(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, logger *log.Logger) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}
	// initialize a new Reconciler
	reconciler := PodReconciler{
		virtualClient:    virtMgr.GetClient(),
		hostClient:       hostMgr.GetClient(),
		Scheme:           virtMgr.GetScheme(),
		HostScheme:       hostMgr.GetScheme(),
		logger:           logger.Named(podController),
		Translator:       translator,
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,
	}
	return ctrl.NewControllerManagedBy(virtMgr).
		For(&v1.Pod{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (r *PodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.clusterName, "clusterNamespace", r.clusterNamespace)
	var (
		virtPod v1.Pod
		cluster v1alpha1.Cluster
	)
	if err := r.hostClient.Get(ctx, types.NamespacedName{Name: r.clusterName, Namespace: r.clusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	// handling pod
	if err := r.virtualClient.Get(ctx, req.NamespacedName, &virtPod); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	// reconcile pods with pvcs
	for _, vol := range virtPod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			log.Info("Handling pod with pvc")
			if err := r.reconcilePodWithPVC(ctx, &virtPod, vol.PersistentVolumeClaim); err != nil {
				return reconcile.Result{}, err
			}
		}
	}
	return reconcile.Result{}, nil
}

// reconcilePodWithPVC will make sure to create a fake PV for each PVC for any pod so that it can be scheduled on the virtual-kubelet
// and then created on the host, the PV is not synced to the host cluster.
func (r *PodReconciler) reconcilePodWithPVC(ctx context.Context, pod *v1.Pod, pvcSource *v1.PersistentVolumeClaimVolumeSource) error {
	log := ctrl.LoggerFrom(ctx).WithValues("PersistentVolumeClaim", pvcSource.ClaimName)
	var (
		pvc v1.PersistentVolumeClaim
	)
	if err := r.virtualClient.Get(ctx, types.NamespacedName{Name: pvcSource.ClaimName, Namespace: pod.Namespace}, &pvc); err != nil {
		return ctrlruntimeclient.IgnoreNotFound(err)
	}
	log.Info("Creating pseudo Persistent Volume")
	pv := r.pseudoPV(&pvc)
	if err := r.virtualClient.Create(ctx, pv); err != nil {
		return ctrlruntimeclient.IgnoreAlreadyExists(err)
	}
	orig := pv.DeepCopy()
	pv.Status = v1.PersistentVolumeStatus{
		Phase: v1.VolumeBound,
	}
	if err := r.virtualClient.Status().Patch(ctx, pv, ctrlruntimeclient.MergeFrom(orig)); err != nil {
		return err
	}

	log.Info("Patch the status of PersistentVolumeClaim to Bound")
	pvcPatch := pvc.DeepCopy()
	if pvcPatch.Annotations == nil {
		pvcPatch.Annotations = make(map[string]string)
	}
	pvcPatch.Annotations[volume.AnnBoundByController] = "yes"
	pvcPatch.Annotations[volume.AnnBindCompleted] = "yes"
	pvcPatch.Status.Phase = v1.ClaimBound
	pvcPatch.Status.AccessModes = pvcPatch.Spec.AccessModes

	return r.virtualClient.Status().Update(ctx, pvcPatch)
}

func (r *PodReconciler) pseudoPV(obj *v1.PersistentVolumeClaim) *v1.PersistentVolume {
	storageClass := ""
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
