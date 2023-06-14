package cluster

import (
	"context"

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
		MaxConcurrentReconciles: 1,
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
				return reconcile.Result{}, util.WrapErr("failed to get cluster namespace "+util.ClusterNamespace(&cluster), err)
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
	// create a new namespace for the cluster
	if err := c.createNamespace(ctx, cluster); err != nil {
		return util.WrapErr("failed to create ns", err)
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
	serviceIP, err := c.createClusterService(ctx, cluster)
	if err != nil {
		return util.WrapErr("failed to create cluster service", err)
	}

	if err := c.createClusterConfigs(ctx, cluster, serviceIP); err != nil {
		return util.WrapErr("failed to create cluster configs", err)
	}

	if err := c.createDeployments(ctx, cluster); err != nil {
		return util.WrapErr("failed to create servers and agents deployment", err)
	}

	if cluster.Spec.Expose != nil {
		if cluster.Spec.Expose.Ingress != nil {
			serverIngress, err := server.Ingress(ctx, cluster, c.Client)
			if err != nil {
				return util.WrapErr("failed to create ingress object", err)
			}

			if err := c.Client.Create(ctx, serverIngress); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					return util.WrapErr("failed to create server ingress", err)
				}
			}
		}
	}

	kubeconfigSecret, err := server.GenerateNewKubeConfig(ctx, cluster, serviceIP)
	if err != nil {
		return util.WrapErr("failed to generate new kubeconfig", err)
	}

	if err := c.Client.Create(ctx, kubeconfigSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.WrapErr("failed to create kubeconfig secret", err)
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
			return util.WrapErr("failed to create ns", err)
		}
	}

	return nil
}

func (c *ClusterReconciler) createClusterConfigs(ctx context.Context, cluster *v1alpha1.Cluster, serviceIP string) error {
	// create init node config
	initServerConfig, err := config.ServerConfig(cluster, true, serviceIP)
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
	serverConfig, err := config.ServerConfig(cluster, false, serviceIP)
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
	agentsConfig := config.AgentConfig(cluster, serviceIP)
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

func (c *ClusterReconciler) createClusterService(ctx context.Context, cluster *v1alpha1.Cluster) (string, error) {
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

func (c *ClusterReconciler) createDeployments(ctx context.Context, cluster *v1alpha1.Cluster) error {
	// create deployment for the init server
	// the init deployment must have only 1 replica
	initServerDeployment := server.Server(cluster, true)

	if err := controllerutil.SetControllerReference(cluster, initServerDeployment, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, initServerDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	// create deployment for the rest of the servers
	serversDeployment := server.Server(cluster, false)

	if err := controllerutil.SetControllerReference(cluster, serversDeployment, c.Scheme); err != nil {
		return err
	}

	if err := c.Client.Create(ctx, serversDeployment); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	agentsDeployment := agent.Agent(cluster)
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

func (c *ClusterReconciler) createCIDRPools(ctx context.Context) error {
	clusterSubnets, err := generateSubnets(defaultClusterCIDR)
	if err != nil {
		return err
	}

	var clusterSubnetAllocations []v1alpha1.Allocation
	for _, cs := range clusterSubnets {
		clusterSubnetAllocations = append(clusterSubnetAllocations, v1alpha1.Allocation{
			IPNet: cs,
		})
	}

	cidrClusterPool := v1alpha1.CIDRAllocationPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: cidrAllocationClusterPoolName,
		},
		Spec: v1alpha1.CIDRAllocationPoolSpec{
			DefaultClusterCIDR: defaultClusterCIDR,
		},
		Status: v1alpha1.CIDRAllocationPoolStatus{
			Pool: clusterSubnetAllocations,
		},
	}
	if err := c.Client.Create(ctx, &cidrClusterPool); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			// return nil since the resource has
			// already been created
			return err
		}
	}

	clusterServiceSubnets, err := generateSubnets(defaultClusterServiceCIDR)
	if err != nil {
		return err
	}

	var clusterServiceSubnetAllocations []v1alpha1.Allocation
	for _, ss := range clusterServiceSubnets {
		clusterServiceSubnetAllocations = append(clusterServiceSubnetAllocations, v1alpha1.Allocation{
			IPNet: ss,
		})
	}

	cidrServicePool := v1alpha1.CIDRAllocationPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: cidrAllocationServicePoolName,
		},
		Spec: v1alpha1.CIDRAllocationPoolSpec{
			DefaultClusterCIDR: defaultClusterCIDR,
		},
		Status: v1alpha1.CIDRAllocationPoolStatus{
			Pool: clusterServiceSubnetAllocations,
		},
	}
	if err := c.Client.Create(ctx, &cidrServicePool); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			// return nil since the resource has
			// already been created
			return err
		}
	}
	return nil
}
