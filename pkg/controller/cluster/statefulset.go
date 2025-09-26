package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	certutil "github.com/rancher/dynamiclistener/cert"
	clientv3 "go.etcd.io/etcd/client/v3"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
	"github.com/rancher/k3k/pkg/controller/cluster/server/bootstrap"
)

const (
	podController = "k3k-pod-controller"
)

type PodReconciler struct {
	Client ctrlruntimeclient.Client
	Scheme *runtime.Scheme
}

// Add adds a new controller to the manager
func AddPodController(ctx context.Context, mgr manager.Manager, maxConcurrentReconciles int) error {
	// initialize a new Reconciler
	reconciler := PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Watches(&v1.Pod{}, handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &apps.StatefulSet{}, handler.OnlyControllerOwner())).
		Named(podController).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Complete(&reconciler)
}

func (p *PodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("statefulset", req.NamespacedName)
	ctx = ctrl.LoggerInto(ctx, log) // enrich the current logger

	s := strings.Split(req.Name, "-")
	if len(s) < 1 {
		return reconcile.Result{}, nil
	}

	if s[0] != "k3k" {
		return reconcile.Result{}, nil
	}

	clusterName := s[1]

	var cluster v1alpha1.Cluster
	if err := p.Client.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: req.Namespace}, &cluster); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
	}

	matchingLabels := ctrlruntimeclient.MatchingLabels(map[string]string{"role": "server"})
	listOpts := &ctrlruntimeclient.ListOptions{Namespace: req.Namespace}
	matchingLabels.ApplyToList(listOpts)

	var podList v1.PodList
	if err := p.Client.List(ctx, &podList, listOpts); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	if len(podList.Items) == 1 {
		return reconcile.Result{}, nil
	}

	for _, pod := range podList.Items {
		if err := p.handleServerPod(ctx, cluster, &pod); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (p *PodReconciler) handleServerPod(ctx context.Context, cluster v1alpha1.Cluster, pod *v1.Pod) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("handling server pod")

	role, found := pod.Labels["role"]
	if !found {
		return fmt.Errorf("server pod has no role label")
	}

	if role != "server" {
		log.V(1).Info("pod has a different role: " + role)
		return nil
	}

	// if etcd pod is marked for deletion then we need to remove it from the etcd member list before deletion
	if !pod.DeletionTimestamp.IsZero() {
		// check if cluster is deleted then remove the finalizer from the pod
		if cluster.Name == "" {
			if controllerutil.ContainsFinalizer(pod, etcdPodFinalizerName) {
				controllerutil.RemoveFinalizer(pod, etcdPodFinalizerName)

				if err := p.Client.Update(ctx, pod); err != nil {
					return err
				}
			}

			return nil
		}

		tlsConfig, err := p.getETCDTLS(ctx, &cluster)
		if err != nil {
			return err
		}

		// remove server from etcd
		client, err := clientv3.New(clientv3.Config{
			Endpoints: []string{
				fmt.Sprintf("https://%s.%s:2379", server.ServiceName(cluster.Name), pod.Namespace),
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
		if controllerutil.RemoveFinalizer(pod, etcdPodFinalizerName) {
			if err := p.Client.Update(ctx, pod); err != nil {
				return err
			}
		}
	}

	if controllerutil.AddFinalizer(pod, etcdPodFinalizerName) {
		return p.Client.Update(ctx, pod)
	}

	return nil
}

func (p *PodReconciler) getETCDTLS(ctx context.Context, cluster *v1alpha1.Cluster) (*tls.Config, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("generating etcd TLS client certificate", "cluster", cluster)

	token, err := p.clusterToken(ctx, cluster)
	if err != nil {
		return nil, err
	}

	endpoint := server.ServiceName(cluster.Name) + "." + cluster.Namespace

	var b *bootstrap.ControlRuntimeBootstrap

	if err := retry.OnError(k3kcontroller.Backoff, func(err error) bool {
		return true
	}, func() error {
		var err error
		b, err = bootstrap.DecodedBootstrap(token, endpoint)
		return err
	}); err != nil {
		return nil, err
	}

	etcdCert, etcdKey, err := certs.CreateClientCertKey("etcd-client", nil, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, 0, b.ETCDServerCA.Content, b.ETCDServerCAKey.Content)
	if err != nil {
		return nil, err
	}

	clientCert, err := tls.X509KeyPair(etcdCert, etcdKey)
	if err != nil {
		return nil, err
	}
	// create rootCA CertPool
	cert, err := certutil.ParseCertsPEM([]byte(b.ETCDServerCA.Content))
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

// removePeer removes a peer from the cluster. The peer name and IP address must both match.
func removePeer(ctx context.Context, client *clientv3.Client, name, address string) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("removing peer from cluster", "name", name, "address", address)

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
				log.Info("removing member from etcd", "name", member.Name, "id", member.ID, "address", address)

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

func (p *PodReconciler) clusterToken(ctx context.Context, cluster *v1alpha1.Cluster) (string, error) {
	var tokenSecret v1.Secret

	nn := types.NamespacedName{
		Name:      TokenSecretName(cluster.Name),
		Namespace: cluster.Namespace,
	}

	if cluster.Spec.TokenSecretRef != nil {
		nn.Name = TokenSecretName(cluster.Name)
	}

	if err := p.Client.Get(ctx, nn, &tokenSecret); err != nil {
		return "", err
	}

	if _, ok := tokenSecret.Data["token"]; !ok {
		return "", fmt.Errorf("no token field in secret %s/%s", nn.Namespace, nn.Name)
	}

	return string(tokenSecret.Data["token"]), nil
}
