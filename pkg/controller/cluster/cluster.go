package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	certutil "github.com/rancher/dynamiclistener/cert"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/cluster/config"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/util"
	"github.com/sirupsen/logrus"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
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
	etcdPodFinalizerName = "etcdpod.k3k.io/finalizer"
	ClusterInvalidName   = "system"

	maxConcurrentReconciles = 1

	defaultClusterCIDR           = "10.44.0.0/16"
	defaultClusterServiceCIDR    = "10.45.0.0/16"
	defaultStoragePersistentSize = "1G"
	memberRemovalTimeout         = time.Minute * 1
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

	if err := controller.Watch(&source.Kind{Type: &v1alpha1.Cluster{}}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return controller.Watch(&source.Kind{Type: &v1.Pod{}},
		&handler.EnqueueRequestForOwner{IsController: true, OwnerType: &apps.StatefulSet{}})
}

func (c *ClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {

	var (
		cluster     v1alpha1.Cluster
		podList     v1.PodList
		clusterName string
	)
	if req.Namespace != "" {
		s := strings.Split(req.Namespace, "-")
		if len(s) <= 1 {
			return reconcile.Result{}, util.LogAndReturnErr("failed to get cluster namespace", nil)
		}

		clusterName = s[1]
		var cluster v1alpha1.Cluster
		if err := c.Client.Get(ctx, types.NamespacedName{Name: clusterName}, &cluster); err != nil {
			return reconcile.Result{}, util.LogAndReturnErr("failed to get cluster object", err)
		}
		if *cluster.Spec.Servers == 1 {
			klog.Infof("skipping request for etcd pod for cluster [%s] since it is not in HA mode", clusterName)
			return reconcile.Result{}, nil
		}
		matchingLabels := client.MatchingLabels(map[string]string{"role": "server"})
		listOpts := &client.ListOptions{Namespace: req.Namespace}
		matchingLabels.ApplyToList(listOpts)

		if err := c.Client.List(ctx, &podList, listOpts); err != nil {
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
		for _, pod := range podList.Items {
			klog.Infof("Handle etcd server pod [%s/%s]", pod.Namespace, pod.Name)
			if err := c.handleServerPod(ctx, cluster, &pod); err != nil {
				return reconcile.Result{}, util.LogAndReturnErr("failed to handle etcd pod", err)
			}
		}

		return reconcile.Result{}, nil
	}

	if err := c.Client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&cluster, clusterFinalizerName) {
			controllerutil.AddFinalizer(&cluster, clusterFinalizerName)
			if err := c.Client.Update(ctx, &cluster); err != nil {
				return reconcile.Result{}, util.LogAndReturnErr("failed to add cluster finalizer", err)
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
		if err := c.createCluster(ctx, &cluster); err != nil {
			return reconcile.Result{}, util.LogAndReturnErr("failed to create cluster", err)
		}
		return reconcile.Result{}, nil
	}

	// remove finalizer from the server pods and update them.
	matchingLabels := client.MatchingLabels(map[string]string{"role": "server"})
	listOpts := &client.ListOptions{Namespace: util.ClusterNamespace(&cluster)}
	matchingLabels.ApplyToList(listOpts)

	if err := c.Client.List(ctx, &podList, listOpts); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
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
	if cluster.Name == ClusterInvalidName {
		klog.Errorf("Invalid cluster name %s, no action will be taken", cluster.Name)
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

func (c *ClusterReconciler) handleServerPod(ctx context.Context, cluster v1alpha1.Cluster, pod *v1.Pod) error {
	if _, ok := pod.Labels["role"]; ok {
		if pod.Labels["role"] != "server" {
			return nil
		}
	} else {
		return errors.New("server pod has no role label")
	}
	// if etcd pod is marked for deletion then we need to remove it from the etcd member list before deletion
	if !pod.DeletionTimestamp.IsZero() {
		if cluster.Status.Persistence.Type != server.EphermalNodesType {
			if controllerutil.ContainsFinalizer(pod, etcdPodFinalizerName) {
				controllerutil.RemoveFinalizer(pod, etcdPodFinalizerName)
				if err := c.Client.Update(ctx, pod); err != nil {
					return err
				}
			}
		}
		tlsConfig, err := c.getETCDTLS(&cluster)
		if err != nil {
			return err
		}
		// remove server from etcd
		client, err := clientv3.New(clientv3.Config{
			Endpoints: []string{
				"https://k3k-server-service." + pod.Namespace + ":2379",
			},
			TLS: tlsConfig,
		})
		if err != nil {
			return err
		}

		if err := removePeer(ctx, client, pod.Name, pod.Status.PodIP); err != nil {
			return err
		}
		// remove our finalizer from the list and update it.
		if controllerutil.ContainsFinalizer(pod, etcdPodFinalizerName) {
			controllerutil.RemoveFinalizer(pod, etcdPodFinalizerName)
			if err := c.Client.Update(ctx, pod); err != nil {
				return err
			}
		}
	}
	if !controllerutil.ContainsFinalizer(pod, etcdPodFinalizerName) {
		controllerutil.AddFinalizer(pod, etcdPodFinalizerName)
		return c.Client.Update(ctx, pod)
	}

	return nil
}

// removePeer removes a peer from the cluster. The peer name and IP address must both match.
func removePeer(ctx context.Context, client *clientv3.Client, name, address string) error {
	ctx, cancel := context.WithTimeout(ctx, memberRemovalTimeout)
	defer cancel()
	members, err := client.MemberList(ctx)
	if err != nil {
		return err
	}

	for _, member := range members.Members {
		if !strings.Contains(member.Name, name) {
			continue
		}
		for _, peerURL := range member.PeerURLs {
			u, err := url.Parse(peerURL)
			if err != nil {
				return err
			}
			if u.Hostname() == address {
				logrus.Infof("Removing name=%s id=%d address=%s from etcd", member.Name, member.ID, address)
				_, err := client.MemberRemove(ctx, member.ID)
				if errors.Is(err, rpctypes.ErrGRPCMemberNotFound) {
					return nil
				}
				return err
			}
		}
	}

	return nil
}

func (c *ClusterReconciler) getETCDTLS(cluster *v1alpha1.Cluster) (*tls.Config, error) {
	klog.Infof("generating etcd TLS client certificate for cluster [%s]", cluster.Name)
	token := cluster.Spec.Token
	endpoint := "k3k-server-service." + util.ClusterNamespace(cluster)
	var bootstrap *server.ControlRuntimeBootstrap
	if err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return true
	}, func() error {
		var err error
		bootstrap, err = server.DecodedBootstrap(token, endpoint)
		return err
	}); err != nil {
		return nil, err
	}

	etcdCert, etcdKey, err := server.CreateClientCertKey("etcd-client", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, bootstrap.ETCDServerCA.Content, bootstrap.ETCDServerCAKey.Content)
	if err != nil {
		return nil, err
	}
	clientCert, err := tls.X509KeyPair(etcdCert, etcdKey)
	if err != nil {
		return nil, err
	}
	// create rootCA CertPool
	cert, err := certutil.ParseCertsPEM([]byte(bootstrap.ETCDServerCA.Content))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert[0])

	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCert},
	}, nil
}
