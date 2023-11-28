package cluster

import (
	"context"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/cluster/config"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/util"
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
	clusterController    = "k3k-cluster-controller"
	clusterFinalizerName = "cluster.k3k.io/finalizer"

	maxConcurrentReconciles = 1

	defaultClusterCIDR        = "10.44.0.0/16"
	defaultClusterServiceCIDR = "10.45.0.0/16"
)

type ClusterReconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager) error {
	// initialize a new Reconciler
	reconciler := ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	// create a new controller and add it to the manager
	//this can be replaced by the new builder functionality in controller-runtime
	controller, err := controller.New(clusterController, mgr, controller.Options{
		Reconciler:              &reconciler,
		MaxConcurrentReconciles: maxConcurrentReconciles,
	})
	if err != nil {
		return err
	}

	return controller.Watch(&source.Kind{Type: &v1alpha1.Cluster{}}, &handler.EnqueueRequestForObject{})
}

func (c *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var cluster v1alpha1.Cluster

	if err := c.Client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
			controllerutil.AddFinalizer(&cluster, clusterFinalizerName)
			if err := c.Client.Update(ctx, &cluster); err != nil {
				return reconcile.Result{}, err
			}
		}

		// we create a namespace for each new cluster
		var ns v1.Namespace
		objKey := client.ObjectKey{
			Name: util.ClusterNamespace(&cluster),
		}
		if err := c.Client.Get(ctx, objKey, &ns); err != nil {
			if !apierrors.IsNotFound(err) {
				return reconcile.Result{}, util.LogAndReturnErr("failed to get cluster namespace "+util.ClusterNamespace(&cluster), err)
			}
		}

		klog.Infof("enqueue cluster [%s]", cluster.Name)

		return reconcile.Result{}, c.createCluster(ctx, &cluster)
	}

	if controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
		// remove our finalizer from the list and update it.
		controllerutil.RemoveFinalizer(&cluster, clusterFinalizerName)
		if err := c.Client.Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, err
		}
	}
	klog.Infof("deleting cluster [%s]", cluster.Name)

	return reconcile.Result{}, nil
}

func (c *ClusterReconciler) createCluster(ctx context.Context, cluster *v1alpha1.Cluster) error {
	s := server.New(cluster, c.Client)

	if cluster.Spec.Persistence == nil {
		// default to ephermal nodes
		cluster.Spec.Persistence = &v1alpha1.PersistenceConfig{
			Type: server.EphermalNodesType,
		}
	}
	if err := c.Client.Update(ctx, cluster); err != nil {
		return util.LogAndReturnErr("failed to update cluster with persistence type", err)
	}
	// create a new namespace for the cluster
	if err := c.createNamespace(ctx, cluster); err != nil {
		return util.LogAndReturnErr("failed to create ns", err)
	}

	cluster.Status.ClusterCIDR = cluster.Spec.ClusterCIDR
	if cluster.Status.ClusterCIDR == "" {
		cluster.Status.ClusterCIDR = defaultClusterCIDR
	}

	cluster.Status.ServiceCIDR = cluster.Spec.ServiceCIDR
	if cluster.Status.ServiceCIDR == "" {
		cluster.Status.ServiceCIDR = defaultClusterServiceCIDR
	}

	klog.Infof("creating cluster service")
	serviceIP, err := c.createClusterService(ctx, cluster, s)
	if err != nil {
		return util.LogAndReturnErr("failed to create cluster service", err)
	}

	if err := c.createClusterConfigs(ctx, cluster, serviceIP); err != nil {
		return util.LogAndReturnErr("failed to create cluster configs", err)
	}

	// creating statefulsets in case the user chose a persistence type other than ephermal

	if cluster.Spec.Persistence.StorageRequestSize == "" {
		// default to 1G of request size
		cluster.Spec.Persistence.StorageRequestSize = "1G"
	}
	if err := c.server(ctx, cluster, s); err != nil {
		return util.LogAndReturnErr("failed to create servers", err)
	}

	if err := c.agent(ctx, cluster); err != nil {
		return util.LogAndReturnErr("failed to create agents", err)
	}

	if cluster.Spec.Expose != nil {
		if cluster.Spec.Expose.Ingress != nil {
			serverIngress, err := s.Ingress(ctx, c.Client)
			if err != nil {
				return util.LogAndReturnErr("failed to create ingress object", err)
			}

			if err := c.Client.Create(ctx, serverIngress); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					return util.LogAndReturnErr("failed to create server ingress", err)
				}
			}
		}
	}

	kubeconfigSecret, err := s.GenerateNewKubeConfig(ctx, serviceIP)
	if err != nil {
		return util.LogAndReturnErr("failed to generate new kubeconfig", err)
	}

	if err := c.Client.Create(ctx, kubeconfigSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.LogAndReturnErr("failed to create kubeconfig secret", err)
		}
	}

	return c.Client.Update(ctx, cluster)
}

func (c *ClusterReconciler) createNamespace(ctx context.Context, cluster *v1alpha1.Cluster) error {
	// create a new namespace for the cluster
	namespace := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: util.ClusterNamespace(cluster),
		},
	}
	if err := controllerutil.SetControllerReference(cluster, &namespace, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, &namespace); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.LogAndReturnErr("failed to create ns", err)
		}
	}

	return nil
}

func (c *ClusterReconciler) createClusterConfigs(ctx context.Context, cluster *v1alpha1.Cluster, serviceIP string) error {
	// create init node config
	initServerConfig, err := config.Server(cluster, true, serviceIP)
	if err != nil {
		return err
	}

	if err := controllerutil.SetControllerReference(cluster, initServerConfig, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, initServerConfig); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	// create servers configuration
	serverConfig, err := config.Server(cluster, false, serviceIP)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(cluster, serverConfig, c.Scheme); err != nil {
		return err
	}
	if err := c.Client.Create(ctx, serverConfig); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	// create agents configuration
	agentsConfig := agentConfig(cluster, serviceIP)
	if err := controllerutil.SetControllerReference(cluster, &agentsConfig, c.Scheme); err != nil {
		return err
	}
	if err := c.Client.Create(ctx, &agentsConfig); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	return nil
}

func (c *ClusterReconciler) createClusterService(ctx context.Context, cluster *v1alpha1.Cluster, server *server.Server) (string, error) {
	// create cluster service
	clusterService := server.Service(cluster)

	if err := controllerutil.SetControllerReference(cluster, clusterService, c.Scheme); err != nil {
		return "", err
	}
	if err := c.Client.Create(ctx, clusterService); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", err
		}
	}

	var service v1.Service

	objKey := client.ObjectKey{
		Namespace: util.ClusterNamespace(cluster),
		Name:      "k3k-server-service",
	}
	if err := c.Client.Get(ctx, objKey, &service); err != nil {
		return "", err
	}

	return service.Spec.ClusterIP, nil
}

func (c *ClusterReconciler) server(ctx context.Context, cluster *v1alpha1.Cluster, server *server.Server) error {
	// create headless service for the init statefulset
	ServerStatefulService := server.StatefulServerService(cluster)
	if err := controllerutil.SetControllerReference(cluster, ServerStatefulService, c.Scheme); err != nil {
		return err
	}
	if err := c.Client.Create(ctx, ServerStatefulService); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	ServerStatefulSet, err := server.StatefulServer(ctx, cluster)
	if err != nil {
		return err
	}

	if err := controllerutil.SetControllerReference(cluster, ServerStatefulSet, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, ServerStatefulSet); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	return nil
}

func (c *ClusterReconciler) agent(ctx context.Context, cluster *v1alpha1.Cluster) error {
	agent := agent.New(cluster)

	agentsDeployment := agent.Deploy()
	if err := controllerutil.SetControllerReference(cluster, agentsDeployment, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, agentsDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

func serverData(serviceIP string, cluster *v1alpha1.Cluster) string {
	return "cluster-init: true\nserver: https://" + serviceIP + ":6443" + serverOptions(cluster)
}

func initConfigData(cluster *v1alpha1.Cluster) string {
	return "cluster-init: true\n" + serverOptions(cluster)
}

func serverOptions(cluster *v1alpha1.Cluster) string {
	var opts string

	// TODO: generate token if not found
	if cluster.Spec.Token != "" {
		opts = "token: " + cluster.Spec.Token + "\n"
	}
	if cluster.Status.ClusterCIDR != "" {
		opts = opts + "cluster-cidr: " + cluster.Status.ClusterCIDR + "\n"
	}
	if cluster.Status.ServiceCIDR != "" {
		opts = opts + "service-cidr: " + cluster.Status.ServiceCIDR + "\n"
	}
	if cluster.Spec.ClusterDNS != "" {
		opts = opts + "cluster-dns: " + cluster.Spec.ClusterDNS + "\n"
	}
	if len(cluster.Spec.TLSSANs) > 0 {
		opts = opts + "tls-san:\n"
		for _, addr := range cluster.Spec.TLSSANs {
			opts = opts + "- " + addr + "\n"
		}
	}
	// TODO: Add extra args to the options

	return opts
}

func agentConfig(cluster *v1alpha1.Cluster, serviceIP string) v1.Secret {
	config := agentData(serviceIP, cluster.Spec.Token)

	return v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "k3k-agent-config",
			Namespace: util.ClusterNamespace(cluster),
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}
}

func agentData(serviceIP, token string) string {
	return fmt.Sprintf(`server: https://%s:6443
token: %s`, serviceIP, token)
}

// func serverNamesConfig(cluster *v1alpha1.Cluster) v1.ConfigMap {
// 	servers := cluster.Replicas
// 	return v1.ConfigMap{
// 		TypeMeta: metav1.TypeMeta{
// 			Kind:       "Secret",
// 			APIVersion: "v1",
// 		},
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      "k3k-agent-config",
// 			Namespace: util.ClusterNamespace(cluster),
// 		},
// 		Data: map[string]string{},
// 	}
// }
