package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
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
	DiscoveryClient            *discovery.DiscoveryClient
	Client                     ctrlruntimeclient.Client
	Scheme                     *runtime.Scheme
	SharedAgentImage           string
	SharedAgentImagePullPolicy string
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager, sharedAgentImage, sharedAgentImagePullPolicy string) error {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	if sharedAgentImage == "" {
		return errors.New("missing shared agent image")
	}

	// initialize a new Reconciler
	reconciler := ClusterReconciler{
		DiscoveryClient:            discoveryClient,
		Client:                     mgr.GetClient(),
		Scheme:                     mgr.GetScheme(),
		SharedAgentImage:           sharedAgentImage,
		SharedAgentImagePullPolicy: sharedAgentImagePullPolicy,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Cluster{}).
		WithOptions(ctrlruntimecontroller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Owns(&apps.StatefulSet{}).
		Complete(&reconciler)
}

func (c *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", req.NamespacedName)
	ctx = ctrl.LoggerInto(ctx, log) // enrich the current logger

	log.Info("reconciling cluster")

	var cluster v1alpha1.Cluster
	if err := c.Client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	// if DeletionTimestamp is not Zero -> finalize the object
	if !cluster.DeletionTimestamp.IsZero() {
		return c.finalizeCluster(ctx, cluster)
	}

	// add finalizers
	if !controllerutil.AddFinalizer(&cluster, clusterFinalizerName) {
		if err := c.Client.Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, err
		}
	}

	orig := cluster.DeepCopy()

	reconcilerErr := c.reconcileCluster(ctx, &cluster)

	// update Status if needed
	if !reflect.DeepEqual(orig.Status, cluster.Status) {
		if err := c.Client.Status().Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, err
		}
	}

	// if there was an error during the reconciliation, return
	if reconcilerErr != nil {
		return reconcile.Result{}, reconcilerErr
	}

	// update Cluster if needed
	if !reflect.DeepEqual(orig.Spec, cluster.Spec) {
		if err := c.Client.Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *ClusterReconciler) reconcileCluster(ctx context.Context, cluster *v1alpha1.Cluster) error {
	log := ctrl.LoggerFrom(ctx)

	// if the Version is not specified we will try to use the same Kubernetes version of the host.
	// This version is stored in the Status object, and it will not be updated if already set.
	if cluster.Spec.Version == "" && cluster.Status.HostVersion == "" {
		log.Info("cluster version not set")

		hostVersion, err := c.DiscoveryClient.ServerVersion()
		if err != nil {
			return err
		}

		// update Status HostVersion
		k8sVersion := strings.Split(hostVersion.GitVersion, "+")[0]
		cluster.Status.HostVersion = k8sVersion + "-k3s1"
	}

	// TODO: update status?
	if err := c.validate(cluster); err != nil {
		log.Error(err, "invalid change")
		return nil
	}

	token, err := c.token(ctx, cluster)
	if err != nil {
		return err
	}

	s := server.New(cluster, c.Client, token, string(cluster.Spec.Mode))

	if cluster.Spec.Persistence != nil {
		cluster.Status.Persistence = cluster.Spec.Persistence
		if cluster.Spec.Persistence.StorageRequestSize == "" {
			// default to 1G of request size
			cluster.Status.Persistence.StorageRequestSize = defaultStoragePersistentSize
		}
	}

	cluster.Status.ClusterCIDR = cluster.Spec.ClusterCIDR
	if cluster.Status.ClusterCIDR == "" {
		cluster.Status.ClusterCIDR = defaultClusterCIDR
	}

	cluster.Status.ServiceCIDR = cluster.Spec.ServiceCIDR
	if cluster.Status.ServiceCIDR == "" {
		cluster.Status.ServiceCIDR = defaultClusterServiceCIDR
	}

	service, err := c.ensureClusterService(ctx, cluster)
	if err != nil {
		return err
	}
	serviceIP := service.Spec.ClusterIP

	if err := c.createClusterConfigs(ctx, cluster, s, serviceIP); err != nil {
		return err
	}

	// creating statefulsets in case the user chose a persistence type other than ephemeral
	if err := c.server(ctx, cluster, s); err != nil {
		return err
	}

	if err := c.ensureAgent(ctx, cluster, serviceIP, token); err != nil {
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

	if err := c.ensureBootstrapSecret(ctx, cluster, serviceIP, token); err != nil {
		return err
	}

	if err := c.bindNodeProxyClusterRole(ctx, cluster); err != nil {
		return err
	}

	return nil
}

// ensureBootstrapSecret will create or update the Secret containing the bootstrap data from the k3s server
func (c *ClusterReconciler) ensureBootstrapSecret(ctx context.Context, cluster *v1alpha1.Cluster, serviceIP, token string) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("ensuring bootstrap secret")

	bootstrapData, err := bootstrap.GenerateBootstrapData(ctx, cluster, serviceIP, token)
	if err != nil {
		return err
	}

	bootstrapSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "bootstrap"),
			Namespace: cluster.Namespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, c.Client, bootstrapSecret, func() error {
		if err := controllerutil.SetControllerReference(cluster, bootstrapSecret, c.Scheme); err != nil {
			return err
		}

		bootstrapSecret.Data = map[string][]byte{
			"bootstrap": bootstrapData,
		}
		return nil
	})
	return err
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

func (c *ClusterReconciler) ensureClusterService(ctx context.Context, cluster *v1alpha1.Cluster) (*v1.Service, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("ensuring cluster service")

	service := server.Service(cluster)

	createdClusterService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.Name,
			Namespace: service.Namespace,
		},
	}
	result, err := controllerutil.CreateOrUpdate(ctx, c.Client, createdClusterService, func() error {
		if err := controllerutil.SetControllerReference(cluster, createdClusterService, c.Scheme); err != nil {
			return err
		}

		createdClusterService.Spec = service.Spec
		return nil
	})
	if err != nil {
		return nil, err
	}

	key := client.ObjectKeyFromObject(createdClusterService)
	if result != controllerutil.OperationResultNone {
		log.Info("ensuring cluster service", "key", key, "result", result)
	}

	return createdClusterService, nil
}

func (c *ClusterReconciler) server(ctx context.Context, cluster *v1alpha1.Cluster, server *server.Server) error {
	log := ctrl.LoggerFrom(ctx)

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

	serverStatefulSet, err := server.StatefulServer(ctx)
	if err != nil {
		return err
	}

	result, err := controllerutil.CreateOrUpdate(ctx, c.Client, serverStatefulSet, func() error {
		return controllerutil.SetControllerReference(cluster, serverStatefulSet, c.Scheme)
	})

	if result != controllerutil.OperationResultNone {
		key := client.ObjectKeyFromObject(serverStatefulSet)
		log.Info("ensuring serverStatefulSet", "key", key, "result", result)
	}

	return err
}

func (c *ClusterReconciler) bindNodeProxyClusterRole(ctx context.Context, cluster *v1alpha1.Cluster) error {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	if err := c.Client.Get(ctx, types.NamespacedName{Name: "k3k-node-proxy"}, clusterRoleBinding); err != nil {
		return fmt.Errorf("failed to get or find k3k-node-proxy ClusterRoleBinding: %w", err)
	}

	subjectName := controller.SafeConcatNameWithPrefix(cluster.Name, agent.SharedNodeAgentName)

	found := false
	for _, subject := range clusterRoleBinding.Subjects {
		if subject.Name == subjectName && subject.Namespace == cluster.Namespace {
			found = true
		}
	}

	if !found {
		clusterRoleBinding.Subjects = append(clusterRoleBinding.Subjects, rbacv1.Subject{
			Kind:      "ServiceAccount",
			Name:      subjectName,
			Namespace: cluster.Namespace,
		})
	}

	return c.Client.Update(ctx, clusterRoleBinding)
}

func (c *ClusterReconciler) ensureAgent(ctx context.Context, cluster *v1alpha1.Cluster, serviceIP, token string) error {
	config := agent.NewConfig(cluster, c.Client, c.Scheme)

	var agentEnsurer agent.ResourceEnsurer
	if cluster.Spec.Mode == agent.VirtualNodeMode {
		agentEnsurer = agent.NewVirtualAgent(config, serviceIP, token)
	} else {
		agentEnsurer = agent.NewSharedAgent(config, serviceIP, c.SharedAgentImage, c.SharedAgentImagePullPolicy, token)
	}

	return agentEnsurer.EnsureResources(ctx)
}

func (c *ClusterReconciler) validate(cluster *v1alpha1.Cluster) error {
	if cluster.Name == ClusterInvalidName {
		return errors.New("invalid cluster name " + cluster.Name + " no action will be taken")
	}
	return nil
}
