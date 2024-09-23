package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/cluster/config"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/rancher/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	clusterController    = "k3k-cluster-controller"
	clusterFinalizerName = "cluster.k3k.io/finalizer"
	etcdPodFinalizerName = "etcdpod.k3k.io/finalizer"
	ClusterInvalidName   = "system"

	maxConcurrentReconciles = 1

	defaultClusterCIDR           = "10.44.0.0/16"
	defaultClusterServiceCIDR    = "10.45.0.0/16"
	defaultStoragePersistentSize = "1G"
	memberRemovalTimeout         = time.Minute * 1
)

type ClusterReconciler struct {
	Client ctrlruntimeclient.Client
	Scheme *runtime.Scheme
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager) error {
	// initialize a new Reconciler
	reconciler := ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Cluster{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Complete(&reconciler)
}

func (c *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {

	var (
		cluster v1alpha1.Cluster
		podList v1.PodList
	)

	if err := c.Client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	if cluster.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
			controllerutil.AddFinalizer(&cluster, clusterFinalizerName)
			if err := c.Client.Update(ctx, &cluster); err != nil {
				return reconcile.Result{}, util.LogAndReturnErr("failed to add cluster finalizer", err)
			}
		}

		klog.Infof("enqueue cluster [%s]", cluster.Name)
		if err := c.createCluster(ctx, &cluster); err != nil {
			return reconcile.Result{}, util.LogAndReturnErr("failed to create cluster", err)
		}
		return reconcile.Result{}, nil
	}

	// remove finalizer from the server pods and update them.
	matchingLabels := ctrlruntimeclient.MatchingLabels(map[string]string{"role": "server"})
	listOpts := &ctrlruntimeclient.ListOptions{Namespace: util.ClusterNamespace(&cluster)}
	matchingLabels.ApplyToList(listOpts)

	if err := c.Client.List(ctx, &podList, listOpts); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	for _, pod := range podList.Items {
		if controllerutil.ContainsFinalizer(&pod, etcdPodFinalizerName) {
			controllerutil.RemoveFinalizer(&pod, etcdPodFinalizerName)
			if err := c.Client.Update(ctx, &pod); err != nil {
				return reconcile.Result{}, util.LogAndReturnErr("failed to remove etcd finalizer", err)
			}
		}
	}

	if controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
		// remove finalizer from the cluster and update it.
		controllerutil.RemoveFinalizer(&cluster, clusterFinalizerName)
		if err := c.Client.Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, util.LogAndReturnErr("failed to remove cluster finalizer", err)
		}
	}
	klog.Infof("deleting cluster [%s]", cluster.Name)

	return reconcile.Result{}, nil
}

func (c *ClusterReconciler) createCluster(ctx context.Context, cluster *v1alpha1.Cluster) error {
	if err := c.validate(cluster); err != nil {
		klog.Errorf("invalid change: %v", err)
		return nil
	}
	s := server.New(cluster, c.Client)

	if cluster.Spec.Persistence != nil {
		cluster.Status.Persistence = cluster.Spec.Persistence
		if cluster.Spec.Persistence.StorageRequestSize == "" {
			// default to 1G of request size
			cluster.Status.Persistence.StorageRequestSize = defaultStoragePersistentSize
		}
	}
	if err := c.Client.Update(ctx, cluster); err != nil {
		return util.LogAndReturnErr("failed to update cluster with persistence type", err)
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

	bootstrapSecret, err := bootstrap.Generate(ctx, cluster, serviceIP)
	if err != nil {
		return util.LogAndReturnErr("failed to generate new kubeconfig", err)
	}

	if err := c.Client.Create(ctx, bootstrapSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return util.LogAndReturnErr("failed to create kubeconfig secret", err)
		}
	}

	return c.Client.Update(ctx, cluster)
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

	objKey := ctrlruntimeclient.ObjectKey{
		Namespace: util.ClusterNamespace(cluster),
		Name:      util.ServerSvcName(cluster),
	}
	if err := c.Client.Get(ctx, objKey, &service); err != nil {
		return "", err
	}

	return service.Spec.ClusterIP, nil
}

func (c *ClusterReconciler) server(ctx context.Context, cluster *v1alpha1.Cluster, server *server.Server) error {
	// create headless service for the statefulset
	serverStatefulService := server.StatefulServerService(cluster)
	if err := controllerutil.SetControllerReference(cluster, serverStatefulService, c.Scheme); err != nil {
		return err
	}
	if err := c.Client.Create(ctx, serverStatefulService); err != nil {
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

	if err := c.ensure(ctx, ServerStatefulSet, false); err != nil {
		return err
	}

	return nil
}

func (c *ClusterReconciler) agent(ctx context.Context, cluster *v1alpha1.Cluster) error {
	agent := agent.New(cluster)

	agentsDeployment := agent.Deploy()
	if err := controllerutil.SetControllerReference(cluster, agentsDeployment, c.Scheme); err != nil {
		return err
	}

	if err := c.ensure(ctx, agentsDeployment, false); err != nil {
		return err
	}
	return nil
}

func agentConfig(cluster *v1alpha1.Cluster, serviceIP string) v1.Secret {
	config := agentData(serviceIP, cluster.Spec.Token)

	return v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.AgentConfigName(cluster),
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

func (c *ClusterReconciler) validate(cluster *v1alpha1.Cluster) error {
	if cluster.Name == ClusterInvalidName {
		return errors.New("invalid cluster name " + cluster.Name + " no action will be taken")
	}
	return nil
}

func (c *ClusterReconciler) ensure(ctx context.Context, obj ctrlruntimeclient.Object, requiresRecreate bool) error {
	exists := true
	existingObject := obj.DeepCopyObject().(ctrlruntimeclient.Object)
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, existingObject); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get Object(%T): %w", existingObject, err)
		}
		exists = false
	}

	if !exists {
		// if not exists create object
		if err := c.Client.Create(ctx, obj); err != nil {
			return err
		}
		return nil
	}
	// if exists then apply udpate or recreate if necessary
	if reflect.DeepEqual(obj.(metav1.Object), existingObject.(metav1.Object)) {
		return nil
	}

	if !requiresRecreate {
		if err := c.Client.Update(ctx, obj); err != nil {
			return err
		}
	} else {
		// this handles object that needs recreation including configmaps and secrets
		if err := c.Client.Delete(ctx, obj); err != nil {
			return err
		}
		if err := c.Client.Create(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}
