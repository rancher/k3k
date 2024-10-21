package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	"github.com/rancher/k3k/pkg/log"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	namePrefix           = "k3k"
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
	Client           ctrlruntimeclient.Client
	Scheme           *runtime.Scheme
	SharedAgentImage string
	logger           *log.Logger
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager, sharedAgentImage string, logger *log.Logger) error {
	// initialize a new Reconciler
	reconciler := ClusterReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		SharedAgentImage: sharedAgentImage,
		logger:           logger.Named(clusterController),
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
	log := c.logger.With("Cluster", req.NamespacedName)
	if err := c.Client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	if cluster.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
			controllerutil.AddFinalizer(&cluster, clusterFinalizerName)
			if err := c.Client.Update(ctx, &cluster); err != nil {
				return reconcile.Result{}, err
			}
		}
		log.Info("enqueue cluster")
		return reconcile.Result{}, c.createCluster(ctx, &cluster, log)
	}

	// remove finalizer from the server pods and update them.
	matchingLabels := ctrlruntimeclient.MatchingLabels(map[string]string{"role": "server"})
	listOpts := &ctrlruntimeclient.ListOptions{Namespace: cluster.Namespace}
	matchingLabels.ApplyToList(listOpts)
	if err := c.Client.List(ctx, &podList, listOpts); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	for _, pod := range podList.Items {
		if controllerutil.ContainsFinalizer(&pod, etcdPodFinalizerName) {
			controllerutil.RemoveFinalizer(&pod, etcdPodFinalizerName)
			if err := c.Client.Update(ctx, &pod); err != nil {
				return reconcile.Result{}, err
			}
		}
	}
	if controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
		// remove finalizer from the cluster and update it.
		controllerutil.RemoveFinalizer(&cluster, clusterFinalizerName)
		if err := c.Client.Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, err
		}
	}
	log.Info("deleting cluster")
	return reconcile.Result{}, nil
}

func (c *ClusterReconciler) createCluster(ctx context.Context, cluster *v1alpha1.Cluster, log *zap.SugaredLogger) error {
	if err := c.validate(cluster); err != nil {
		log.Errorw("invalid change", zap.Error(err))
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
		return err
	}

	cluster.Status.ClusterCIDR = cluster.Spec.ClusterCIDR
	if cluster.Status.ClusterCIDR == "" {
		cluster.Status.ClusterCIDR = defaultClusterCIDR
	}

	cluster.Status.ServiceCIDR = cluster.Spec.ServiceCIDR
	if cluster.Status.ServiceCIDR == "" {
		cluster.Status.ServiceCIDR = defaultClusterServiceCIDR
	}

	log.Info("creating cluster service")
	serviceIP, err := c.createClusterService(ctx, cluster, s)
	if err != nil {
		return err
	}

	if err := c.createClusterConfigs(ctx, cluster, s, serviceIP); err != nil {
		return err
	}

	// creating statefulsets in case the user chose a persistence type other than ephermal
	if err := c.server(ctx, cluster, s); err != nil {
		return err
	}

	if err := c.agent(ctx, cluster, serviceIP); err != nil {
		return err
	}

	if cluster.Spec.Expose != nil {
		if cluster.Spec.Expose.Ingress != nil {
			serverIngress, err := s.Ingress(ctx, c.Client)
			if err != nil {
				return err
			}

			if err := c.Client.Create(ctx, serverIngress); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					return err
				}
			}
		}
	}

	bootstrapSecret, err := bootstrap.Generate(ctx, cluster, serviceIP)
	if err != nil {
		return err
	}

	if err := c.Client.Create(ctx, bootstrapSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	return c.Client.Update(ctx, cluster)
}

func (c *ClusterReconciler) createClusterConfigs(ctx context.Context, cluster *v1alpha1.Cluster, server *server.Server, serviceIP string) error {
	// create init node config
	initServerConfig, err := server.Config(true, serviceIP)
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
	serverConfig, err := server.Config(false, serviceIP)
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

	return nil
}

func (c *ClusterReconciler) createClusterService(ctx context.Context, cluster *v1alpha1.Cluster, s *server.Server) (string, error) {
	// create cluster service
	clusterService := s.Service(cluster)

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
		Namespace: cluster.Namespace,
		Name:      server.ServiceName(cluster.Name),
	}
	if err := c.Client.Get(ctx, objKey, &service); err != nil {
		return "", err
	}

	return service.Spec.ClusterIP, nil
}

func (c *ClusterReconciler) server(ctx context.Context, cluster *v1alpha1.Cluster, server *server.Server) error {
	// create headless service for the statefulset
	serverStatefulService := server.StatefulServerService()
	if err := controllerutil.SetControllerReference(cluster, serverStatefulService, c.Scheme); err != nil {
		return err
	}
	if err := c.Client.Create(ctx, serverStatefulService); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	ServerStatefulSet, err := server.StatefulServer(ctx)
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

func (c *ClusterReconciler) agent(ctx context.Context, cluster *v1alpha1.Cluster, serviceIP string) error {
	agent := agent.New(cluster, serviceIP, c.SharedAgentImage)
	agentsConfig, err := agent.Config()
	if err != nil {
		return err
	}
	agentResources := agent.Resources()
	agentResources = append(agentResources, agentsConfig)

	return c.ensureAll(ctx, cluster, agentResources)
}

func (c *ClusterReconciler) validate(cluster *v1alpha1.Cluster) error {
	if cluster.Name == ClusterInvalidName {
		return errors.New("invalid cluster name " + cluster.Name + " no action will be taken")
	}
	return nil
}

func (c *ClusterReconciler) ensureAll(ctx context.Context, cluster *v1alpha1.Cluster, objs []ctrlruntimeclient.Object) error {
	for _, obj := range objs {
		if err := controllerutil.SetControllerReference(cluster, obj, c.Scheme); err != nil {
			return err
		}
		if err := c.ensure(ctx, obj, false); err != nil {
			return err
		}
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
