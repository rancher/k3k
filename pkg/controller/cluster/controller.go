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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ClusterController    = "k3k-cluster-controller"
	ClusterFinalizerName = "cluster.k3k.io/finalizer"
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
	cluster := &v1alpha1.Cluster{}

	if err := r.Client.Get(ctx, req.NamespacedName, cluster); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(cluster, ClusterFinalizerName) {
			controllerutil.AddFinalizer(cluster, ClusterFinalizerName)
			if err := r.Client.Update(ctx, cluster); err != nil {
				return reconcile.Result{}, err
			}
		}
		// we create a namespace for each new cluster
		ns := &v1.Namespace{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: util.ClusterNamespace(cluster)}, ns); err != nil {
			if !apierrors.IsNotFound(err) {
				return reconcile.Result{},
					util.WrapErr(fmt.Sprintf("failed to get cluster namespace %s", util.ClusterNamespace(cluster)), err)
			}
		}
		klog.Infof("enqueue cluster [%s]", cluster.Name)
		return reconcile.Result{}, r.createCluster(ctx, cluster)
	}
	if controllerutil.ContainsFinalizer(cluster, ClusterFinalizerName) {
		// TODO: handle CIDR deletion

		// remove our finalizer from the list and update it.
		controllerutil.RemoveFinalizer(cluster, ClusterFinalizerName)
		if err := r.Client.Update(ctx, cluster); err != nil {
			return reconcile.Result{}, err
		}
	}
	klog.Infof("deleting cluster [%s]", cluster.Name)
	return reconcile.Result{}, nil
}

func (r *ClusterReconciler) createCluster(ctx context.Context, cluster *v1alpha1.Cluster) error {
	// create a new namespace for the cluster
	if err := r.createNamespace(ctx, cluster); err != nil {
		return util.WrapErr("failed to create ns", err)
	}

	serviceIP, err := r.createClusterService(ctx, cluster)
	if err != nil {
		return util.WrapErr("failed to create cluster service", err)
	}

	if err := r.createClusterConfigs(ctx, cluster, serviceIP); err != nil {
		return util.WrapErr("failed to create cluster configs", err)
	}

	if err := r.createDeployments(ctx, cluster); err != nil {
		return util.WrapErr("failed to create servers and agents deployment", err)
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

	kubeconfigSecret, err := server.GenerateNewKubeConfig(ctx, cluster, serviceIP)
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

func (r *ClusterReconciler) createNamespace(ctx context.Context, cluster *v1alpha1.Cluster) error {
	// create a new namespace for the cluster
	namespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: util.ClusterNamespace(cluster),
		},
	}
	if err := controllerutil.SetControllerReference(cluster, namespace, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, namespace); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create ns", err)
		}
	}

	return nil
}

func (r *ClusterReconciler) createClusterConfigs(ctx context.Context, cluster *v1alpha1.Cluster, serviceIP string) error {
	// create init node config
	initServerConfig, err := config.ServerConfig(cluster, true, serviceIP)
	if err != nil {
		return err
	}

	if err := controllerutil.SetControllerReference(cluster, initServerConfig, r.Scheme); err != nil {
		return err
	}

	if err := r.Client.Create(ctx, initServerConfig); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	// create servers configuration
	serverConfig, err := config.ServerConfig(cluster, false, serviceIP)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(cluster, serverConfig, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, serverConfig); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	// create agents configuration
	agentsConfig := config.AgentConfig(cluster, serviceIP)
	if err := controllerutil.SetControllerReference(cluster, &agentsConfig, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, &agentsConfig); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

func (r *ClusterReconciler) createClusterService(ctx context.Context, cluster *v1alpha1.Cluster) (string, error) {
	// create cluster service
	clusterService := server.Service(cluster)

	if err := controllerutil.SetControllerReference(cluster, clusterService, r.Scheme); err != nil {
		return "", err
	}
	if err := r.Client.Create(ctx, clusterService); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", err
		}
	}

	service := v1.Service{}
	if err := r.Client.Get(ctx,
		client.ObjectKey{
			Namespace: util.ClusterNamespace(cluster),
			Name:      "k3k-server-service"},
		&service); err != nil {
		return "", err
	}

	return service.Spec.ClusterIP, nil
}

func (r *ClusterReconciler) createDeployments(ctx context.Context, cluster *v1alpha1.Cluster) error {
	// create deployment for the init server
	// the init deployment must have only 1 replica
	initServerDeployment := server.Server(cluster, true)

	if err := controllerutil.SetControllerReference(cluster, initServerDeployment, r.Scheme); err != nil {
		return err
	}

	if err := r.Client.Create(ctx, initServerDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	// create deployment for the rest of the servers
	serversDeployment := server.Server(cluster, false)

	if err := controllerutil.SetControllerReference(cluster, serversDeployment, r.Scheme); err != nil {
		return err
	}

	if err := r.Client.Create(ctx, serversDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	agentsDeployment := agent.Agent(cluster)
	if err := controllerutil.SetControllerReference(cluster, agentsDeployment, r.Scheme); err != nil {
		return err
	}

	if err := r.Client.Create(ctx, agentsDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	return nil
}
