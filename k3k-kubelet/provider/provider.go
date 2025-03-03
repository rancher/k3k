package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/rancher/k3k/k3k-kubelet/controller"
	"github.com/rancher/k3k/k3k-kubelet/controller/webhook"
	"github.com/rancher/k3k/k3k-kubelet/provider/collectors"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3klog "github.com/rancher/k3k/pkg/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	cv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"errors"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	compbasemetrics "k8s.io/component-base/metrics"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// check at compile time if the Provider implements the nodeutil.Provider interface
var _ nodeutil.Provider = (*Provider)(nil)

// Provider implements nodetuil.Provider from virtual Kubelet.
// TODO: Implement NotifyPods and the required usage so that this can be an async provider
type Provider struct {
	Handler          controller.ControllerHandler
	Translator       translate.ToHostTranslator
	HostClient       client.Client
	VirtualClient    client.Client
	ClientConfig     rest.Config
	CoreClient       cv1.CoreV1Interface
	ClusterNamespace string
	ClusterName      string
	serverIP         string
	dnsIP            string
	logger           *k3klog.Logger
}

var (
	ErrRetryTimeout = errors.New("provider timed out")
)

func New(hostConfig rest.Config, hostMgr, virtualMgr manager.Manager, logger *k3klog.Logger, namespace, name, serverIP, dnsIP string) (*Provider, error) {
	coreClient, err := cv1.NewForConfig(&hostConfig)
	if err != nil {
		return nil, err
	}

	translator := translate.ToHostTranslator{
		ClusterName:      name,
		ClusterNamespace: namespace,
	}

	p := Provider{
		Handler: controller.ControllerHandler{
			Mgr:           virtualMgr,
			Scheme:        *virtualMgr.GetScheme(),
			HostClient:    hostMgr.GetClient(),
			VirtualClient: virtualMgr.GetClient(),
			Translator:    translator,
			Logger:        logger,
		},
		HostClient:       hostMgr.GetClient(),
		VirtualClient:    virtualMgr.GetClient(),
		Translator:       translator,
		ClientConfig:     hostConfig,
		CoreClient:       coreClient,
		ClusterNamespace: namespace,
		ClusterName:      name,
		logger:           logger,
		serverIP:         serverIP,
		dnsIP:            dnsIP,
	}

	return &p, nil
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	hostPodName := p.Translator.TranslateName(namespace, podName)
	options := corev1.PodLogOptions{
		Container:  containerName,
		Timestamps: opts.Timestamps,
		Follow:     opts.Follow,
		Previous:   opts.Previous,
	}

	if opts.Tail != 0 {
		tailLines := int64(opts.Tail)
		options.TailLines = &tailLines
	}

	if opts.LimitBytes != 0 {
		limitBytes := int64(opts.LimitBytes)
		options.LimitBytes = &limitBytes
	}

	if opts.SinceSeconds != 0 {
		sinceSeconds := int64(opts.SinceSeconds)
		options.SinceSeconds = &sinceSeconds
	}

	if !opts.SinceTime.IsZero() {
		sinceTime := metav1.NewTime(opts.SinceTime)
		options.SinceTime = &sinceTime
	}

	closer, err := p.CoreClient.Pods(p.ClusterNamespace).GetLogs(hostPodName, &options).Stream(ctx)
	p.logger.Infof("got error %s when getting logs for %s in %s", err, hostPodName, p.ClusterNamespace)

	return closer, err
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(ctx context.Context, namespace, podName, containerName string, cmd []string, attach api.AttachIO) error {
	hostPodName := p.Translator.TranslateName(namespace, podName)
	req := p.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(hostPodName).
		Namespace(p.ClusterNamespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   cmd,
		TTY:       attach.TTY(),
		Stdin:     attach.Stdin() != nil,
		Stdout:    attach.Stdout() != nil,
		Stderr:    attach.Stderr() != nil,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(&p.ClientConfig, http.MethodPost, req.URL())
	if err != nil {
		return err
	}

	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  attach.Stdin(),
		Stdout: attach.Stdout(),
		Stderr: attach.Stderr(),
		Tty:    attach.TTY(),
		TerminalSizeQueue: &translatorSizeQueue{
			resizeChan: attach.Resize(),
		},
	})
}

// AttachToContainer attaches to the executing process of a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) AttachToContainer(ctx context.Context, namespace, podName, containerName string, attach api.AttachIO) error {
	hostPodName := p.Translator.TranslateName(namespace, podName)
	req := p.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(hostPodName).
		Namespace(p.ClusterNamespace).
		SubResource("attach")
	req.VersionedParams(&corev1.PodAttachOptions{
		Container: containerName,
		TTY:       attach.TTY(),
		Stdin:     attach.Stdin() != nil,
		Stdout:    attach.Stdout() != nil,
		Stderr:    attach.Stderr() != nil,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(&p.ClientConfig, http.MethodPost, req.URL())
	if err != nil {
		return err
	}

	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  attach.Stdin(),
		Stdout: attach.Stdout(),
		Stderr: attach.Stderr(),
		Tty:    attach.TTY(),
		TerminalSizeQueue: &translatorSizeQueue{
			resizeChan: attach.Resize(),
		},
	})
}

// GetStatsSummary gets the stats for the node, including running pods
func (p *Provider) GetStatsSummary(ctx context.Context) (*statsv1alpha1.Summary, error) {
	p.logger.Debug("GetStatsSummary")

	nodeList := &v1.NodeList{}
	if err := p.CoreClient.RESTClient().Get().Resource("nodes").Do(ctx).Into(nodeList); err != nil {
		return nil, fmt.Errorf("unable to get nodes of cluster %s in namespace %s: %w", p.ClusterName, p.ClusterNamespace, err)
	}

	// fetch the stats from all the nodes
	var (
		nodeStats    statsv1alpha1.NodeStats
		allPodsStats []statsv1alpha1.PodStats
	)

	for _, n := range nodeList.Items {
		res, err := p.CoreClient.RESTClient().
			Get().
			Resource("nodes").
			Name(n.Name).
			SubResource("proxy").
			Suffix("stats/summary").
			DoRaw(ctx)
		if err != nil {
			return nil, fmt.Errorf(
				"unable to get stats of node '%s', from cluster %s in namespace %s: %w",
				n.Name, p.ClusterName, p.ClusterNamespace, err,
			)
		}

		stats := &statsv1alpha1.Summary{}
		if err := json.Unmarshal(res, stats); err != nil {
			return nil, err
		}

		// TODO: we should probably calculate somehow the node stats from the different nodes of the host
		// or reflect different nodes from the virtual kubelet.
		// For the moment let's just pick one random node stats.
		nodeStats = stats.Node
		allPodsStats = append(allPodsStats, stats.Pods...)
	}

	pods, err := p.GetPods(ctx)
	if err != nil {
		return nil, err
	}

	podsNameMap := make(map[string]*v1.Pod)

	for _, pod := range pods {
		hostPodName := p.Translator.TranslateName(pod.Namespace, pod.Name)
		podsNameMap[hostPodName] = pod
	}

	filteredStats := &statsv1alpha1.Summary{
		Node: nodeStats,
		Pods: make([]statsv1alpha1.PodStats, 0),
	}

	for _, podStat := range allPodsStats {
		// skip pods that are not in the cluster namespace
		if podStat.PodRef.Namespace != p.ClusterNamespace {
			continue
		}

		// rewrite the PodReference to match the data of the virtual cluster
		if pod, found := podsNameMap[podStat.PodRef.Name]; found {
			podStat.PodRef = statsv1alpha1.PodReference{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				UID:       string(pod.UID),
			}
			filteredStats.Pods = append(filteredStats.Pods, podStat)
		}
	}

	return filteredStats, nil
}

// GetMetricsResource gets the metrics for the node, including running pods
func (p *Provider) GetMetricsResource(ctx context.Context) ([]*dto.MetricFamily, error) {
	statsSummary, err := p.GetStatsSummary(ctx)
	if err != nil {
		return nil, errors.Join(err, errors.New("error fetching MetricsResource"))
	}

	registry := compbasemetrics.NewKubeRegistry()
	registry.CustomMustRegister(collectors.NewKubeletResourceMetricsCollector(statsSummary))

	metricFamily, err := registry.Gather()
	if err != nil {
		return nil, errors.Join(err, errors.New("error gathering metrics from collector"))
	}

	return metricFamily, nil
}

// PortForward forwards a local port to a port on the pod
func (p *Provider) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	hostPodName := p.Translator.TranslateName(namespace, pod)
	req := p.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(hostPodName).
		Namespace(p.ClusterNamespace).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(&p.ClientConfig)
	if err != nil {
		return err
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())
	portAsString := strconv.Itoa(int(port))
	readyChannel := make(chan struct{})
	stopChannel := make(chan struct{}, 1)

	// Today this doesn't work properly. When the port ward is supposed to stop, the caller (this provider)
	// should send a value on stopChannel so that the PortForward is stopped. However, we only have a ReadWriteCloser
	// so more work is needed to detect a close and handle that appropriately.
	fw, err := portforward.New(dialer, []string{portAsString}, stopChannel, readyChannel, stream, stream)

	if err != nil {
		return err
	}

	return fw.ForwardPorts()
}

// CreatePod executes createPod with retry
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	return p.withRetry(ctx, p.createPod, pod)
}

// createPod takes a Kubernetes Pod and deploys it within the provider.
func (p *Provider) createPod(ctx context.Context, pod *corev1.Pod) error {
	tPod := pod.DeepCopy()
	p.Translator.TranslateTo(tPod)

	// get Cluster definition
	clusterKey := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      p.ClusterName,
	}

	var cluster v1alpha1.Cluster

	if err := p.HostClient.Get(ctx, clusterKey, &cluster); err != nil {
		return fmt.Errorf("unable to get cluster %s in namespace %s: %w", p.ClusterName, p.ClusterNamespace, err)
	}

	// these values shouldn't be set on create
	tPod.UID = ""
	tPod.ResourceVersion = ""

	// the node was scheduled on the virtual kubelet, but leaving it this way will make it pending indefinitely
	tPod.Spec.NodeName = ""

	tPod.Spec.NodeSelector = cluster.Spec.NodeSelector

	// setting the hostname for the pod if its not set
	if pod.Spec.Hostname == "" {
		tPod.Spec.Hostname = pod.Name
	}

	// if the priorityCluss for the virtual cluster is set then override the provided value
	// Note: the core-dns and local-path-provisioner pod are scheduled by k3s with the
	// 'system-cluster-critical' and 'system-node-critical' default priority classes.
	if cluster.Spec.PriorityClass != "" {
		tPod.Spec.PriorityClassName = cluster.Spec.PriorityClass
		tPod.Spec.Priority = nil
	}

	// fieldpath annotations
	if err := p.configureFieldPathEnv(pod, tPod); err != nil {
		return fmt.Errorf("unable to fetch fieldpath annotations for pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	// volumes will often refer to resources in the virtual cluster, but instead need to refer to the sync'd
	// host cluster version
	if err := p.transformVolumes(ctx, pod.Namespace, tPod.Spec.Volumes); err != nil {
		return fmt.Errorf("unable to sync volumes for pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	// sync serviceaccount token to a the host cluster
	if err := p.transformTokens(ctx, pod, tPod); err != nil {
		return fmt.Errorf("unable to transform tokens for pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}

	// inject networking information to the pod including the virtual cluster controlplane endpoint
	configureNetworking(tPod, pod.Name, pod.Namespace, p.serverIP, p.dnsIP)

	p.logger.Infow("creating pod",
		"host_namespace", tPod.Namespace, "host_name", tPod.Name,
		"virtual_namespace", pod.Namespace, "virtual_name", pod.Name,
	)

	// set ownerReference to the cluster object
	if err := controllerutil.SetControllerReference(&cluster, tPod, p.HostClient.Scheme()); err != nil {
		return err
	}

	return p.HostClient.Create(ctx, tPod)
}

// withRetry retries passed function with interval and timeout
func (p *Provider) withRetry(ctx context.Context, f func(context.Context, *v1.Pod) error, pod *v1.Pod) error {
	const (
		interval = 2 * time.Second
		timeout  = 10 * time.Second
	)

	var allErrors error

	// retryFn will retry until the operation succeed, or the timeout occurs
	retryFn := func(ctx context.Context) (bool, error) {
		if lastErr := f(ctx, pod); lastErr != nil {
			// log that the retry failed?
			allErrors = errors.Join(allErrors, lastErr)
			return false, nil
		}

		return true, nil
	}

	if err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, retryFn); err != nil {
		return errors.Join(allErrors, ErrRetryTimeout)
	}

	return nil
}

// transformVolumes changes the volumes to the representation in the host cluster. Will return an error
// if one/more volumes couldn't be transformed
func (p *Provider) transformVolumes(ctx context.Context, podNamespace string, volumes []corev1.Volume) error {
	for _, volume := range volumes {
		var optional bool

		if strings.HasPrefix(volume.Name, kubeAPIAccessPrefix) {
			continue
		}
		// note: this needs to handle downward api volumes as well, but more thought is needed on how to do that
		if volume.ConfigMap != nil {
			if volume.ConfigMap.Optional != nil {
				optional = *volume.ConfigMap.Optional
			}

			if err := p.syncConfigmap(ctx, podNamespace, volume.ConfigMap.Name, optional); err != nil {
				return fmt.Errorf("unable to sync configmap volume %s: %w", volume.Name, err)
			}

			volume.ConfigMap.Name = p.Translator.TranslateName(podNamespace, volume.ConfigMap.Name)
		} else if volume.Secret != nil {
			if volume.Secret.Optional != nil {
				optional = *volume.Secret.Optional
			}

			if err := p.syncSecret(ctx, podNamespace, volume.Secret.SecretName, optional); err != nil {
				return fmt.Errorf("unable to sync secret volume %s: %w", volume.Name, err)
			}

			volume.Secret.SecretName = p.Translator.TranslateName(podNamespace, volume.Secret.SecretName)
		} else if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap != nil {
					if source.ConfigMap.Optional != nil {
						optional = *source.ConfigMap.Optional
					}

					configMapName := source.ConfigMap.Name
					if err := p.syncConfigmap(ctx, podNamespace, configMapName, optional); err != nil {
						return fmt.Errorf("unable to sync projected configmap %s: %w", configMapName, err)
					}

					source.ConfigMap.Name = p.Translator.TranslateName(podNamespace, configMapName)
				} else if source.Secret != nil {
					if source.Secret.Optional != nil {
						optional = *source.Secret.Optional
					}

					secretName := source.Secret.Name
					if err := p.syncSecret(ctx, podNamespace, secretName, optional); err != nil {
						return fmt.Errorf("unable to sync projected secret %s: %w", secretName, err)
					}
				}
			}
		} else if volume.PersistentVolumeClaim != nil {
			volume.PersistentVolumeClaim.ClaimName = p.Translator.TranslateName(podNamespace, volume.PersistentVolumeClaim.ClaimName)
		} else if volume.DownwardAPI != nil {
			for _, downwardAPI := range volume.DownwardAPI.Items {
				if downwardAPI.FieldRef.FieldPath == translate.MetadataNameField {
					downwardAPI.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.ResourceNameAnnotation)
				}

				if downwardAPI.FieldRef.FieldPath == translate.MetadataNamespaceField {
					downwardAPI.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.ResourceNamespaceAnnotation)
				}
			}
		}
	}

	return nil
}

// syncConfigmap will add the configmap object to the queue of the syncer controller to be synced to the host cluster
func (p *Provider) syncConfigmap(ctx context.Context, podNamespace string, configMapName string, optional bool) error {
	var configMap corev1.ConfigMap

	nsName := types.NamespacedName{
		Namespace: podNamespace,
		Name:      configMapName,
	}

	if err := p.VirtualClient.Get(ctx, nsName, &configMap); err != nil {
		// check if its optional configmap
		if apierrors.IsNotFound(err) && optional {
			return nil
		}

		return fmt.Errorf("unable to get configmap to sync %s/%s: %w", nsName.Namespace, nsName.Name, err)
	}

	if err := p.Handler.AddResource(ctx, &configMap); err != nil {
		return fmt.Errorf("unable to add configmap to sync %s/%s: %w", nsName.Namespace, nsName.Name, err)
	}

	return nil
}

// syncSecret will add the secret object to the queue of the syncer controller to be synced to the host cluster
func (p *Provider) syncSecret(ctx context.Context, podNamespace string, secretName string, optional bool) error {
	p.logger.Infow("Syncing secret", "Name", secretName, "Namespace", podNamespace, "optional", optional)

	var secret corev1.Secret

	nsName := types.NamespacedName{
		Namespace: podNamespace,
		Name:      secretName,
	}

	if err := p.VirtualClient.Get(ctx, nsName, &secret); err != nil {
		if apierrors.IsNotFound(err) && optional {
			return nil
		}

		return fmt.Errorf("unable to get secret to sync %s/%s: %w", nsName.Namespace, nsName.Name, err)
	}

	if err := p.Handler.AddResource(ctx, &secret); err != nil {
		return fmt.Errorf("unable to add secret to sync %s/%s: %w", nsName.Namespace, nsName.Name, err)
	}

	return nil
}

// UpdatePod executes updatePod with retry
func (p *Provider) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	return p.withRetry(ctx, p.updatePod, pod)
}

func (p *Provider) updatePod(ctx context.Context, pod *v1.Pod) error {
	p.logger.Debugw("got a request for update pod")

	// Once scheduled a Pod cannot update other fields than the image of the containers, initcontainers and a few others
	// See: https://kubernetes.io/docs/concepts/workloads/pods/#pod-update-and-replacement

	// Update Pod in the virtual cluster

	var currentVirtualPod v1.Pod
	if err := p.VirtualClient.Get(ctx, client.ObjectKeyFromObject(pod), &currentVirtualPod); err != nil {
		return fmt.Errorf("unable to get pod to update from virtual cluster: %w", err)
	}

	currentVirtualPod.Spec.Containers = updateContainerImages(currentVirtualPod.Spec.Containers, pod.Spec.Containers)
	currentVirtualPod.Spec.InitContainers = updateContainerImages(currentVirtualPod.Spec.InitContainers, pod.Spec.InitContainers)

	currentVirtualPod.Spec.ActiveDeadlineSeconds = pod.Spec.ActiveDeadlineSeconds
	currentVirtualPod.Spec.Tolerations = pod.Spec.Tolerations

	// in the virtual cluster we can update also the labels and annotations
	currentVirtualPod.Annotations = pod.Annotations
	currentVirtualPod.Labels = pod.Labels

	if err := p.VirtualClient.Update(ctx, &currentVirtualPod); err != nil {
		return fmt.Errorf("unable to update pod in the virtual cluster: %w", err)
	}

	// Update Pod in the host cluster

	hostNamespaceName := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      p.Translator.TranslateName(pod.Namespace, pod.Name),
	}

	var currentHostPod corev1.Pod
	if err := p.HostClient.Get(ctx, hostNamespaceName, &currentHostPod); err != nil {
		return fmt.Errorf("unable to get pod to update from host cluster: %w", err)
	}

	currentHostPod.Spec.Containers = updateContainerImages(currentHostPod.Spec.Containers, pod.Spec.Containers)
	currentHostPod.Spec.InitContainers = updateContainerImages(currentHostPod.Spec.InitContainers, pod.Spec.InitContainers)

	// update ActiveDeadlineSeconds and Tolerations
	currentHostPod.Spec.ActiveDeadlineSeconds = pod.Spec.ActiveDeadlineSeconds
	currentHostPod.Spec.Tolerations = pod.Spec.Tolerations

	if err := p.HostClient.Update(ctx, &currentHostPod); err != nil {
		return fmt.Errorf("unable to update pod in the host cluster: %w", err)
	}

	return nil
}

// updateContainerImages will update the images of the original container images with the same name
func updateContainerImages(original, updated []v1.Container) []v1.Container {
	newImages := make(map[string]string)

	for _, c := range updated {
		newImages[c.Name] = c.Image
	}

	for i, c := range original {
		if updatedImage, found := newImages[c.Name]; found {
			original[i].Image = updatedImage
		}
	}

	return original
}

// DeletePod executes deletePod with retry
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	return p.withRetry(ctx, p.deletePod, pod)
}

// deletePod takes a Kubernetes Pod and deletes it from the provider. Once a pod is deleted, the provider is
// expected to call the NotifyPods callback with a terminal pod status where all the containers are in a terminal
// state, as well as the pod. DeletePod may be called multiple times for the same pod.
func (p *Provider) deletePod(ctx context.Context, pod *corev1.Pod) error {
	p.logger.Infof("Got request to delete pod %s", pod.Name)
	hostName := p.Translator.TranslateName(pod.Namespace, pod.Name)

	err := p.CoreClient.Pods(p.ClusterNamespace).Delete(ctx, hostName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("unable to delete pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}

	if err = p.pruneUnusedVolumes(ctx, pod); err != nil {
		// note that we don't return an error here. The pod was successfully deleted, another process
		// should clean this without affecting the user
		p.logger.Errorf("failed to prune leftover volumes for %s/%s: %w, resources may be left", pod.Namespace, pod.Name, err)
	}

	p.logger.Infof("Deleted pod %s", pod.Name)

	return nil
}

// pruneUnusedVolumes removes volumes in use by pod that aren't used by any other pods
func (p *Provider) pruneUnusedVolumes(ctx context.Context, pod *corev1.Pod) error {
	rawSecrets, rawConfigMaps := getSecretsAndConfigmaps(pod)
	// since this pod was removed, originally mark all of the secrets/configmaps it uses as eligible
	// for pruning
	pruneSecrets := sets.Set[string]{}.Insert(rawSecrets...)
	pruneConfigMap := sets.Set[string]{}.Insert(rawConfigMaps...)

	var pods corev1.PodList
	// only pods in the same namespace could be using secrets/configmaps that this pod is using
	err := p.VirtualClient.List(ctx, &pods, &client.ListOptions{
		Namespace: pod.Namespace,
	})
	if err != nil {
		return fmt.Errorf("unable to list pods: %w", err)
	}

	for _, vPod := range pods.Items {
		if vPod.Name == pod.Name {
			continue
		}

		secrets, configMaps := getSecretsAndConfigmaps(&vPod)
		pruneSecrets.Delete(secrets...)
		pruneConfigMap.Delete(configMaps...)
	}

	for _, secretName := range pruneSecrets.UnsortedList() {
		var secret corev1.Secret

		key := types.NamespacedName{
			Name:      secretName,
			Namespace: pod.Namespace,
		}

		if err := p.VirtualClient.Get(ctx, key, &secret); err != nil {
			return fmt.Errorf("unable to get secret %s/%s for pod volume: %w", pod.Namespace, secretName, err)
		}

		if err = p.Handler.RemoveResource(ctx, &secret); err != nil {
			return fmt.Errorf("unable to remove secret %s/%s for pod volume: %w", pod.Namespace, secretName, err)
		}
	}

	for _, configMapName := range pruneConfigMap.UnsortedList() {
		var configMap corev1.ConfigMap

		key := types.NamespacedName{
			Name:      configMapName,
			Namespace: pod.Namespace,
		}

		if err := p.VirtualClient.Get(ctx, key, &configMap); err != nil {
			return fmt.Errorf("unable to get configMap %s/%s for pod volume: %w", pod.Namespace, configMapName, err)
		}

		if err = p.Handler.RemoveResource(ctx, &configMap); err != nil {
			return fmt.Errorf("unable to remove configMap %s/%s for pod volume: %w", pod.Namespace, configMapName, err)
		}
	}

	return nil
}

// GetPod retrieves a pod by name from the provider (can be cached).
// The Pod returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	p.logger.Debugw("got a request for get pod", "Namespace", namespace, "Name", name)
	hostNamespaceName := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      p.Translator.TranslateName(namespace, name),
	}

	var pod corev1.Pod

	if err := p.HostClient.Get(ctx, hostNamespaceName, &pod); err != nil {
		return nil, fmt.Errorf("error when retrieving pod: %w", err)
	}

	p.Translator.TranslateFrom(&pod)

	return &pod, nil
}

// GetPodStatus retrieves the status of a pod by name from the provider.
// The PodStatus returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	p.logger.Debugw("got a request for pod status", "Namespace", namespace, "Name", name)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("unable to get pod for status: %w", err)
	}

	p.logger.Debugw("got pod status", "Namespace", namespace, "Name", name, "Status", pod.Status)

	return pod.Status.DeepCopy(), nil
}

// GetPods retrieves a list of all pods running on the provider (can be cached).
// The Pods returned are expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPods(ctx context.Context) ([]*corev1.Pod, error) {
	selector := labels.NewSelector()

	requirement, err := labels.NewRequirement(translate.ClusterNameLabel, selection.Equals, []string{p.ClusterName})
	if err != nil {
		return nil, fmt.Errorf("unable to create label selector: %w", err)
	}

	selector = selector.Add(*requirement)

	var podList corev1.PodList
	err = p.HostClient.List(ctx, &podList, &client.ListOptions{LabelSelector: selector})

	if err != nil {
		return nil, fmt.Errorf("unable to list pods: %w", err)
	}

	retPods := []*corev1.Pod{}

	for _, pod := range podList.DeepCopy().Items {
		p.Translator.TranslateFrom(&pod)
		retPods = append(retPods, &pod)
	}

	return retPods, nil
}

// configureNetworking will inject network information to each pod to connect them to the
// virtual cluster api server, as well as confiugre DNS information to connect them to the
// synced coredns on the host cluster.
func configureNetworking(pod *corev1.Pod, podName, podNamespace, serverIP, dnsIP string) {
	// inject serverIP to hostalias for the pod
	pod.Spec.HostAliases = append(pod.Spec.HostAliases, corev1.HostAlias{
		IP: serverIP,
		Hostnames: []string{
			"kubernetes",
			"kubernetes.default",
			"kubernetes.default.svc",
			"kubernetes.default.svc.cluster",
			"kubernetes.default.svc.cluster.local",
		},
	})

	// injecting cluster DNS IP to the pods except for coredns pod
	if !strings.HasPrefix(podName, "coredns") {
		pod.Spec.DNSPolicy = corev1.DNSNone
		pod.Spec.DNSConfig = &corev1.PodDNSConfig{
			Nameservers: []string{
				dnsIP,
			},
			Searches: []string{
				podNamespace + ".svc.cluster.local",
				"svc.cluster.local",
				"cluster.local",
			},
		}
	}

	updatedEnvVars := []corev1.EnvVar{
		{Name: "KUBERNETES_PORT", Value: "tcp://" + serverIP + ":6443"},
		{Name: "KUBERNETES_SERVICE_HOST", Value: serverIP},
		{Name: "KUBERNETES_SERVICE_PORT", Value: "6443"},
		{Name: "KUBERNETES_SERVICE_PORT_HTTPS", Value: "6443"},
		{Name: "KUBERNETES_PORT_443_TCP", Value: "tcp://" + serverIP + ":6443"},
		{Name: "KUBERNETES_PORT_443_TCP_ADDR", Value: serverIP},
		{Name: "KUBERNETES_PORT_443_TCP_PORT", Value: "6443"},
	}

	// inject networking information to the pod's environment variables
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].Env = overrideEnvVars(pod.Spec.Containers[i].Env, updatedEnvVars)
	}

	// handle init containers as well
	for i := range pod.Spec.InitContainers {
		pod.Spec.InitContainers[i].Env = overrideEnvVars(pod.Spec.InitContainers[i].Env, updatedEnvVars)
	}
}

// overrideEnvVars will override the orig environment variables if found in the updated list
func overrideEnvVars(orig, updated []corev1.EnvVar) []corev1.EnvVar {
	if len(updated) == 0 {
		return orig
	}

	// create map for single lookup
	updatedEnvVarMap := make(map[string]corev1.EnvVar)
	for _, updatedEnvVar := range updated {
		updatedEnvVarMap[updatedEnvVar.Name] = updatedEnvVar
	}

	for i, origEnvVar := range orig {
		if updatedEnvVar, found := updatedEnvVarMap[origEnvVar.Name]; found {
			orig[i] = updatedEnvVar
		}
	}

	return orig
}

// getSecretsAndConfigmaps retrieves a list of all secrets/configmaps that are in use by a given pod. Useful
// for removing/seeing which virtual cluster resources need to be in the host cluster.
func getSecretsAndConfigmaps(pod *corev1.Pod) ([]string, []string) {
	var (
		secrets    []string
		configMaps []string
	)

	for _, volume := range pod.Spec.Volumes {
		if volume.Secret != nil {
			secrets = append(secrets, volume.Secret.SecretName)
		} else if volume.ConfigMap != nil {
			configMaps = append(configMaps, volume.ConfigMap.Name)
		} else if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap != nil {
					configMaps = append(configMaps, source.ConfigMap.Name)
				} else if source.Secret != nil {
					secrets = append(secrets, source.Secret.Name)
				}
			}
		}
	}

	return secrets, configMaps
}

// configureFieldPathEnv will retrieve all annotations created by the pod mutator webhook
// to assign env fieldpaths to pods, it will also make sure to change the metadata.name and metadata.namespace to the
// assigned annotations
func (p *Provider) configureFieldPathEnv(pod, tPod *v1.Pod) error {
	// override metadata.name and metadata.namespace with pod annotations
	for i, container := range pod.Spec.InitContainers {
		for j, envVar := range container.Env {
			if envVar.ValueFrom == nil || envVar.ValueFrom.FieldRef == nil {
				continue
			}

			fieldPath := envVar.ValueFrom.FieldRef.FieldPath

			if fieldPath == translate.MetadataNameField {
				envVar.ValueFrom.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.ResourceNameAnnotation)
				pod.Spec.InitContainers[i].Env[j] = envVar
			}

			if fieldPath == translate.MetadataNamespaceField {
				envVar.ValueFrom.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.MetadataNamespaceField)
				pod.Spec.InitContainers[i].Env[j] = envVar
			}
		}
	}

	for i, container := range pod.Spec.Containers {
		for j, envVar := range container.Env {
			if envVar.ValueFrom == nil || envVar.ValueFrom.FieldRef == nil {
				continue
			}

			fieldPath := envVar.ValueFrom.FieldRef.FieldPath
			if fieldPath == translate.MetadataNameField {
				envVar.ValueFrom.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.ResourceNameAnnotation)
				pod.Spec.Containers[i].Env[j] = envVar
			}

			if fieldPath == translate.MetadataNamespaceField {
				envVar.ValueFrom.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.ResourceNameAnnotation)
				pod.Spec.Containers[i].Env[j] = envVar
			}
		}
	}

	for name, value := range pod.Annotations {
		if strings.Contains(name, webhook.FieldpathField) {
			containerIndex, envName, err := webhook.ParseFieldPathAnnotationKey(name)
			if err != nil {
				return err
			}
			// re-adding these envs to the pod
			tPod.Spec.Containers[containerIndex].Env = append(tPod.Spec.Containers[containerIndex].Env, v1.EnvVar{
				Name: envName,
				ValueFrom: &v1.EnvVarSource{
					FieldRef: &v1.ObjectFieldSelector{
						FieldPath: value,
					},
				},
			})
			// removing the annotation from the pod
			delete(tPod.Annotations, name)
		}
	}

	return nil
}
