package snapshot

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	k3sv1 "github.com/k3s-io/api/k3s.cattle.io/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/k3s"
)

const (
	snapshotController    = "k3k-snapshot-controller"
	snapshotFinalizerName = "snapshot.k3k.io/finalizer"
)

type SnapshotReconciler struct {
	Client        client.Client
	K8sClient     *kubernetes.Clientset
	RestConfig    *rest.Config
	Scheme        *runtime.Scheme
	PortAllocator *agent.PortAllocator

	record.EventRecorder
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager, maxConcurrentReconciles int, eventRecorder record.EventRecorder) error {
	if eventRecorder == nil {
		eventRecorder = mgr.GetEventRecorderFor(snapshotController)
	}

	restConfig := mgr.GetConfig()

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	// initialize a new Reconciler
	reconciler := SnapshotReconciler{
		Client:        mgr.GetClient(),
		K8sClient:     clientset,
		RestConfig:    restConfig,
		Scheme:        mgr.GetScheme(),
		EventRecorder: eventRecorder,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.ETCDSnapshot{}).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Complete(&reconciler)
}

func (r *SnapshotReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := log.FromContext(ctx)

	log.V(1).Info("reconciling snapshot")

	var snapshot v1beta1.ETCDSnapshot
	if err := r.Client.Get(ctx, req.NamespacedName, &snapshot); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	var cluster v1beta1.Cluster

	nn := types.NamespacedName{
		Name:      snapshot.Spec.ClusterName,
		Namespace: snapshot.Namespace,
	}

	if err := r.Client.Get(ctx, nn, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if cluster.Status.Phase != v1beta1.ClusterReady {
		return reconcile.Result{}, fmt.Errorf("cluster is not ready")
	}

	// avoid recreation of snapshot if its already created
	if snapshot.Status.Address != "" && snapshot.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	token, err := k3kcluster.GetClusterToken(ctx, r.Client, &cluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	var s3Config *k3s.EtcdS3

	if snapshot.Spec.S3ConfigSecret != nil {
		var s3Secret corev1.Secret

		secretKey := types.NamespacedName{
			Name:      snapshot.Spec.S3ConfigSecret.Name,
			Namespace: snapshot.Spec.S3ConfigSecret.Namespace,
		}

		if err := r.Client.Get(ctx, secretKey, &s3Secret); err != nil {
			return reconcile.Result{}, err
		}

		s3Config, err = getS3ConfigFromSecret(&s3Secret, log)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// only request the snapshot from the first server
	endpoint := controller.SafeConcatNameWithPrefix(cluster.Name, "server-0") +
		"." +
		server.HeadlessServiceName(cluster.Name) +
		"." +
		cluster.Namespace +
		":6443"

	client := k3s.New(k3s.ClientConfig{
		Token:    token,
		ServerIP: endpoint,
	})

	// if DeletionTimestamp is not Zero -> finalize the object
	if !snapshot.DeletionTimestamp.IsZero() {
		if snapshot.Status.Address != "" {
			// remove the snapshot from k3s cluster
			log.Info("Deleting snapshot from cluster", "Address", snapshot.Status.Address)

			snapshotResp, err := client.DeleteSnapshot(&snapshot, s3Config)
			if err != nil {
				return reconcile.Result{}, err
			}

			if len(snapshotResp.Deleted) <= 0 {
				return reconcile.Result{}, fmt.Errorf("failed to delete snapshot")
			}
		}

		if controllerutil.RemoveFinalizer(&snapshot, snapshotFinalizerName) {
			if err := r.Client.Update(ctx, &snapshot); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	if controllerutil.AddFinalizer(&snapshot, snapshotFinalizerName) {
		if err := r.Client.Update(ctx, &snapshot); err != nil {
			return reconcile.Result{}, err
		}
	}

	snapshotResp, err := client.SaveSnapshot(&snapshot, s3Config)
	if err != nil {
		return reconcile.Result{}, err
	}

	if len(snapshotResp.Created) <= 0 {
		return reconcile.Result{}, fmt.Errorf("no snapshot found")
	}

	// handling backpopulation
	return reconcile.Result{}, r.backpopulateSnapshotStatus(ctx, snapshotResp.Created[0], &snapshot, client, s3Config)
}

func (r *SnapshotReconciler) backpopulateSnapshotStatus(ctx context.Context, snapshotName string, snapshot *v1beta1.ETCDSnapshot, k3sClient *k3s.Client, s3Config *k3s.EtcdS3) error {
	var (
		snapshotFileList *k3sv1.ETCDSnapshotFileList
		snapshotFile     *k3sv1.ETCDSnapshotFile
	)

	snapshotFileList, err := k3sClient.ListSnapshots(s3Config)
	if err != nil {
		return err
	}

	for _, file := range snapshotFileList.Items {
		if file.Spec.SnapshotName == snapshotName {
			snapshotFile = &file
		}
	}

	if snapshotFile == nil {
		return fmt.Errorf("snapshot file %s not found in virtual cluster", snapshotName)
	}

	snapshot.Status = v1beta1.ETCDSnapshotStatus{
		Address:      snapshotFile.Spec.Location,
		CreationTime: snapshotFile.Status.CreationTime,
		ReadyToUse:   snapshotFile.Status.ReadyToUse,
		Size:         snapshotFile.Status.Size,
		Error:        snapshotFile.Status.Error,
	}

	return r.Client.Status().Update(ctx, snapshot)
}
