package cluster

import (
	"context"
	"fmt"

	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/galal-hussein/k3k/pkg/controller/cluster/agent"
	"github.com/galal-hussein/k3k/pkg/controller/cluster/config"
	"github.com/galal-hussein/k3k/pkg/controller/cluster/server"
	"github.com/galal-hussein/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ClusterController = "k3k-cluster-controller"
)

type ClusterReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// Add adds a new controller to the manager
func Add(mgr manager.Manager) error {
	// initialize a new Reconciler
	reconciler := ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	// create a new controller and add it to the manager
	//this can be replaced by the new builder functionality in controller-runtime
	controller, err := controller.New(ClusterController, mgr, controller.Options{
		Reconciler:              &reconciler,
		MaxConcurrentReconciles: 1,
	})

	if err != nil {
		return err
	}

	if err := controller.Watch(&source.Kind{Type: &v1alpha1.Cluster{}},
		&handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}

func (r *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	cluster := v1alpha1.Cluster{}

	if err := r.Client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return reconcile.Result{}, util.WrapErr(fmt.Sprintf("failed to get cluster %s", req.NamespacedName), err)
	}

	if !cluster.DeletionTimestamp.IsZero() {
		if err := r.handleDeletion(ctx, &cluster); err != nil {
			return reconcile.Result{}, util.WrapErr(fmt.Sprintf("failed to delete cluster %s", req.NamespacedName), err)
		}
	}

	// we create a namespace for each new cluster
	ns := &v1.Namespace{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: util.ClusterNamespace(&cluster)}, ns); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{},
				util.WrapErr(fmt.Sprintf("failed to get cluster namespace %s", util.ClusterNamespace(&cluster)), err)
		}
	}
	klog.Infof("enqueue cluster [%s]", cluster.Name)
	return reconcile.Result{}, r.createCluster(ctx, &cluster)
}

// handleDeletion will delete the k3k cluster from kubernetes
func (r *ClusterReconciler) handleDeletion(ctx context.Context, cluster *v1alpha1.Cluster) error {
	return nil
}

func (r *ClusterReconciler) createCluster(ctx context.Context, cluster *v1alpha1.Cluster) error {
	// create a new namespace for the cluster
	namespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: util.ClusterNamespace(cluster),
		},
	}
	if err := r.Client.Create(ctx, namespace); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create ns", err)
		}

	}

	// create cluster service
	clusterService := server.Service(cluster)
	if err := r.Client.Create(ctx, &clusterService); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create cluster service", err)
		}
	}

	service := v1.Service{}
	if err := r.Client.Get(ctx,
		client.ObjectKey{
			Namespace: util.ClusterNamespace(cluster),
			Name:      "k3k-server-service"},
		&service); err != nil {
		return util.WrapErr("failed to get cluster service", err)
	}

	// create init node config
	initServerConfigMap, err := config.ServerConfig(cluster, true, service.Spec.ClusterIP)
	if err != nil {
		return util.WrapErr("failed to get init server config", err)
	}
	if err := r.Client.Create(ctx, initServerConfigMap); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create init configmap", err)
		}
	}

	// create servers configuration
	serverConfigMap, err := config.ServerConfig(cluster, false, service.Spec.ClusterIP)
	if err != nil {
		return util.WrapErr("failed to get server config", err)

	}
	if err := r.Client.Create(ctx, serverConfigMap); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create configmap", err)
		}
	}

	// create deployment for the init server
	// the init deployment must have only 1 replica
	initNodeDeployment := server.Server(cluster, true)
	if err := r.Client.Create(ctx, initNodeDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create init node deployment", err)
		}
	}

	// create deployment for the rest of the servers
	serverNodesDeployment := server.Server(cluster, false)
	if err := r.Client.Create(ctx, serverNodesDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create server nodes deployment", err)
		}
	}

	agentsConfigMap := config.AgentConfig(cluster, service.Spec.ClusterIP)
	if err := r.Client.Create(ctx, &agentsConfigMap); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create agent config", err)
		}
	}

	agentsDeployment := agent.Agent(cluster)
	if err := r.Client.Create(ctx, agentsDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create agent deployment", err)
		}
	}

	if cluster.Spec.Expose.Ingress.Enabled {
		serverIngress, err := server.Ingress(ctx, cluster, r.Client)
		if err != nil {
			return util.WrapErr("failed to create ingress object", err)
		}
		if err := r.Client.Create(ctx, serverIngress); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return util.WrapErr("failed to create server ingress", err)
			}
		}
	}

	kubeconfigSecret, err := server.GenerateNewKubeConfig(ctx, cluster, service.Spec.ClusterIP)
	if err != nil {
		return util.WrapErr("failed to generate new kubeconfig", err)
	}
	if err := r.Client.Create(ctx, kubeconfigSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create kubeconfig secret", err)
		}
	}
	return nil
}
