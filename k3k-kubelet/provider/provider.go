package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	compbasemetrics "k8s.io/component-base/metrics"
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"

	"github.com/rancher/k3k/k3k-kubelet/controller/webhook"
	"github.com/rancher/k3k/k3k-kubelet/provider/collectors"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcontroller "github.com/rancher/k3k/pkg/controller"
)

// check at compile time if the Provider implements the nodeutil.Provider interface
var _ nodeutil.Provider = (*Provider)(nil)

// Provider implements nodetuil.Provider from virtual Kubelet.
// TODO: Implement NotifyPods and the required usage so that this can be an async provider
type Provider struct {
	Translator       translate.ToHostTranslator
	HostClient       client.Client
	VirtualClient    client.Client
	VirtualManager   manager.Manager
	ClientConfig     rest.Config
	CoreClient       cv1.CoreV1Interface
	ClusterNamespace string
	ClusterName      string
	serverIP         string
	dnsIP            string
	logger           logr.Logger
}

var ErrRetryTimeout = errors.New("provider timed out")

func New(hostConfig rest.Config, hostMgr, virtualMgr manager.Manager, logger logr.Logger, namespace, name, serverIP, dnsIP string) (*Provider, error) {
	coreClient, err := cv1.NewForConfig(&hostConfig)
	if err != nil {
		return nil, err
	}

	translator := translate.ToHostTranslator{
		ClusterName:      name,
		ClusterNamespace: namespace,
	}

	p := Provider{
		HostClient:       hostMgr.GetClient(),
		VirtualClient:    virtualMgr.GetClient(),
		VirtualManager:   virtualMgr,
		Translator:       translator,
		ClientConfig:     hostConfig,
		CoreClient:       coreClient,
		ClusterNamespace: namespace,
		ClusterName:      name,
		logger:           logger.WithValues("cluster", name),
		serverIP:         serverIP,
		dnsIP:            dnsIP,
	}

	return &p, nil
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, name, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	hostPodName := p.Translator.TranslateName(namespace, name)

	logger := p.logger.WithValues("namespace", namespace, "name", name, "pod", hostPodName, "container", containerName)
	logger.V(1).Info("GetContainerLogs")

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
	if err != nil {
		logger.Error(err, "Error getting logs from container")
	}

	return closer, err
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(ctx context.Context, namespace, name, containerName string, cmd []string, attach api.AttachIO) error {
	hostPodName := p.Translator.TranslateName(namespace, name)

	logger := p.logger.WithValues("namespace", namespace, "name", name, "pod", hostPodName, "container", containerName)
	logger.V(1).Info("RunInContainer")

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
		logger.Error(err, "Error creating SPDY executor")
		return err
	}

	if err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  attach.Stdin(),
		Stdout: attach.Stdout(),
		Stderr: attach.Stderr(),
		Tty:    attach.TTY(),
		TerminalSizeQueue: &translatorSizeQueue{
			resizeChan: attach.Resize(),
		},
	}); err != nil {
		logger.Error(err, "Error while executing command in container")
		return err
	}

	return nil
}

// AttachToContainer attaches to the executing process of a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) AttachToContainer(ctx context.Context, namespace, name, containerName string, attach api.AttachIO) error {
	hostPodName := p.Translator.TranslateName(namespace, name)

	logger := p.logger.WithValues("namespace", namespace, "name", name, "pod", hostPodName, "container", containerName)
	logger.V(1).Info("AttachToContainer")

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
		logger.Error(err, "Error creating SPDY executor")
		return err
	}

	if err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  attach.Stdin(),
		Stdout: attach.Stdout(),
		Stderr: attach.Stderr(),
		Tty:    attach.TTY(),
		TerminalSizeQueue: &translatorSizeQueue{
			resizeChan: attach.Resize(),
		},
	}); err != nil {
		logger.Error(err, "Error while attaching to container")
		return err
	}

	return nil
}

// GetStatsSummary gets the stats for the node, including running pods
func (p *Provider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	p.logger.V(1).Info("GetStatsSummary")

	nodeList := &corev1.NodeList{}
	if err := p.CoreClient.RESTClient().Get().Resource("nodes").Do(ctx).Into(nodeList); err != nil {
		p.logger.Error(err, "Unable to get nodes of cluster")
		return nil, err
	}

	// fetch the stats from all the nodes
	var (
		nodeStats    stats.NodeStats
		allPodsStats []stats.PodStats
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
			p.logger.Error(err, "Unable to get stats/summary from cluster node", "node", n.Name)
			return nil, err
		}

		stats := &stats.Summary{}
		if err := json.Unmarshal(res, stats); err != nil {
			p.logger.Error(err, "Error unmarshaling stats/summary from cluster node", "node", n.Name)
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
		p.logger.Error(err, "Error getting pods from cluster for stats")
		return nil, err
	}

	podsNameMap := make(map[string]*corev1.Pod)

	for _, pod := range pods {
		hostPodName := p.Translator.TranslateName(pod.Namespace, pod.Name)
		podsNameMap[hostPodName] = pod
	}

	filteredStats := &stats.Summary{
		Node: nodeStats,
		Pods: make([]stats.PodStats, 0),
	}

	for _, podStat := range allPodsStats {
		// skip pods that are not in the cluster namespace
		if podStat.PodRef.Namespace != p.ClusterNamespace {
			continue
		}

		// rewrite the PodReference to match the data of the virtual cluster
		if pod, found := podsNameMap[podStat.PodRef.Name]; found {
			podStat.PodRef = stats.PodReference{
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
	p.logger.V(1).Info("GetMetricsResource")

	statsSummary, err := p.GetStatsSummary(ctx)
	if err != nil {
		p.logger.Error(err, "Error getting stats summary from cluster for metrics")
		return nil, err
	}

	registry := compbasemetrics.NewKubeRegistry()
	registry.CustomMustRegister(collectors.NewKubeletResourceMetricsCollector(statsSummary))

	metricFamily, err := registry.Gather()
	if err != nil {
		p.logger.Error(err, "Error gathering metrics from collector")
		return nil, err
	}

	return metricFamily, nil
}

// PortForward forwards a local port to a port on the pod
func (p *Provider) PortForward(ctx context.Context, namespace, name string, port int32, stream io.ReadWriteCloser) error {
	hostPodName := p.Translator.TranslateName(namespace, name)

	logger := p.logger.WithValues("namespace", namespace, "name", name, "pod", hostPodName, "port", port)
	logger.V(1).Info("PortForward")

	req := p.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(hostPodName).
		Namespace(p.ClusterNamespace).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(&p.ClientConfig)
	if err != nil {
		logger.Error(err, "Error creating RoundTripper for PortForward")
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
		logger.Error(err, "Error creating new PortForward")
		return err
	}

	if err := fw.ForwardPorts(); err != nil {
		logger.Error(err, "Error forwarding ports")
		return err
	}

	return nil
}

// CreatePod executes createPod with retry
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	return p.withRetry(ctx, p.createPod, pod)
}

func (p *Provider) createPod(ctx context.Context, pod *corev1.Pod) error {
	logger := p.logger.WithValues("namespace", pod.Namespace, "name", pod.Name)
	logger.V(1).Info("CreatePod")

	// fieldPath envs are not being translated correctly using the virtual kubelet pod controller
	// as a workaround we will try to fetch the pod from the virtual cluster and copy over the envSource
	var sourcePod corev1.Pod
	if err := p.VirtualClient.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, &sourcePod); err != nil {
		logger.Error(err, "Error getting Pod from Virtual Cluster")
		return err
	}

	tPod := sourcePod.DeepCopy()
	p.Translator.TranslateTo(tPod)

	logger = p.logger.WithValues("pod", tPod.Name)

	// get Cluster definition
	clusterKey := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      p.ClusterName,
	}

	var cluster v1beta1.Cluster
	if err := p.HostClient.Get(ctx, clusterKey, &cluster); err != nil {
		logger.Error(err, "Error getting Virtual Cluster definition")
		return err
	}

	// get Pod from Virtual Cluster
	key := types.NamespacedName{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}

	var virtualPod corev1.Pod
	if err := p.VirtualClient.Get(ctx, key, &virtualPod); err != nil {
		logger.Error(err, "Getting Pod from Virtual Cluster")
		return err
	}

	// Copy the virtual Pod and use it as a baseline for the hostPod
	// do some basic translation and clearing some values (UID, ResourceVersion, ...)

	hostPod := virtualPod.DeepCopy()
	p.Translator.TranslateTo(hostPod)

	logger = logger.WithValues("host_namespace", hostPod.Namespace, "host_name", hostPod.Name)

	// the node was scheduled on the virtual kubelet, but leaving it this way will make it pending indefinitely
	hostPod.Spec.NodeName = ""

	hostPod.Spec.NodeSelector = cluster.Spec.NodeSelector

	// setting the hostname for the pod if its not set
	if virtualPod.Spec.Hostname == "" {
		hostPod.Spec.Hostname = k3kcontroller.SafeConcatName(virtualPod.Name)
	}

	// if the priorityClass for the virtual cluster is set then override the provided value
	// Note: the core-dns and local-path-provisioner pod are scheduled by k3s with the
	// 'system-cluster-critical' and 'system-node-critical' default priority classes.
	if !strings.HasPrefix(hostPod.Spec.PriorityClassName, "system-") {
		if hostPod.Spec.PriorityClassName != "" {
			tPriorityClassName := p.Translator.TranslateName("", hostPod.Spec.PriorityClassName)
			hostPod.Spec.PriorityClassName = tPriorityClassName
		}

		if cluster.Spec.PriorityClass != "" {
			hostPod.Spec.PriorityClassName = cluster.Spec.PriorityClass
			hostPod.Spec.Priority = nil
		}
	}

	configurePodEnvs(hostPod, &virtualPod)

	// fieldpath annotations
	if err := p.configureFieldPathEnv(&sourcePod, hostPod); err != nil {
		logger.Error(err, "Unable to fetch fieldpath annotations for pod")
		return err
	}

	// volumes will often refer to resources in the virtual cluster
	// but instead need to refer to the synced host cluster version
	p.transformVolumes(pod.Namespace, tPod.Spec.Volumes)

	// sync serviceaccount token to a the host cluster
	if err := p.transformTokens(ctx, &virtualPod, hostPod); err != nil {
		logger.Error(err, "Unable to transform tokens for pod")
		return err
	}

	for i, imagePullSecret := range hostPod.Spec.ImagePullSecrets {
		hostPod.Spec.ImagePullSecrets[i].Name = p.Translator.TranslateName(virtualPod.Namespace, imagePullSecret.Name)
	}

	// inject networking information to the pod including the virtual cluster controlplane endpoint
	configureNetworking(hostPod, virtualPod.Name, virtualPod.Namespace, p.serverIP, p.dnsIP)

	// set ownerReference to the cluster object
	if err := controllerutil.SetControllerReference(&cluster, hostPod, p.HostClient.Scheme()); err != nil {
		logger.Error(err, "Unable to set owner reference for pod")
		return err
	}

	if err := p.HostClient.Create(ctx, hostPod); err != nil {
		logger.Error(err, "Error creating pod on host cluster")
		return err
	}

	logger.Info("Pod created on host cluster")

	return nil
}

// withRetry retries passed function with interval and timeout
func (p *Provider) withRetry(ctx context.Context, f func(context.Context, *corev1.Pod) error, pod *corev1.Pod) error {
	const (
		interval = time.Second
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
func (p *Provider) transformVolumes(podNamespace string, volumes []corev1.Volume) {
	for _, volume := range volumes {
		if strings.HasPrefix(volume.Name, kubeAPIAccessPrefix) {
			continue
		}
		// note: this needs to handle downward api volumes as well, but more thought is needed on how to do that
		if volume.ConfigMap != nil {
			volume.ConfigMap.Name = p.Translator.TranslateName(podNamespace, volume.ConfigMap.Name)
		} else if volume.Secret != nil {
			volume.Secret.SecretName = p.Translator.TranslateName(podNamespace, volume.Secret.SecretName)
		} else if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap != nil {
					source.ConfigMap.Name = p.Translator.TranslateName(podNamespace, source.ConfigMap.Name)
				} else if source.Secret != nil {
					source.Secret.Name = p.Translator.TranslateName(podNamespace, source.Secret.Name)
				}
			}
		} else if volume.PersistentVolumeClaim != nil {
			volume.PersistentVolumeClaim.ClaimName = p.Translator.TranslateName(podNamespace, volume.PersistentVolumeClaim.ClaimName)
		} else if volume.DownwardAPI != nil {
			for _, downwardAPI := range volume.DownwardAPI.Items {
				if downwardAPI.FieldRef != nil {
					if downwardAPI.FieldRef.FieldPath == translate.MetadataNameField {
						downwardAPI.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.ResourceNameAnnotation)
					}

					if downwardAPI.FieldRef.FieldPath == translate.MetadataNamespaceField {
						downwardAPI.FieldRef.FieldPath = fmt.Sprintf("metadata.annotations['%s']", translate.ResourceNamespaceAnnotation)
					}
				}
			}
		}
	}
}

// UpdatePod executes updatePod with retry
func (p *Provider) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	return p.withRetry(ctx, p.updatePod, pod)
}

func (p *Provider) updatePod(ctx context.Context, pod *corev1.Pod) error {
	// Once scheduled a Pod cannot update other fields than the image of the containers, initcontainers and a few others
	// See: https://kubernetes.io/docs/concepts/workloads/pods/#pod-update-and-replacement
	hostPodName := p.Translator.TranslateName(pod.Namespace, pod.Name)

	logger := p.logger.WithValues("namespace", pod.Namespace, "name", pod.Name, "pod", hostPodName)
	logger.V(1).Info("UpdatePod")

	//
	//	Host Pod update
	//

	hostKey := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      hostPodName,
	}

	var hostPod corev1.Pod
	if err := p.HostClient.Get(ctx, hostKey, &hostPod); err != nil {
		logger.Error(err, "Unable to get Pod to update from host cluster")
		return err
	}

	updatePod(&hostPod, pod)

	if err := p.HostClient.Update(ctx, &hostPod); err != nil {
		logger.Error(err, "Unable to update Pod in host cluster")
		return err
	}

	// Ephemeral containers update (subresource)
	if !cmp.Equal(hostPod.Spec.EphemeralContainers, pod.Spec.EphemeralContainers) {
		logger.V(1).Info("Updating ephemeral containers in host pod")

		hostPod.Spec.EphemeralContainers = pod.Spec.EphemeralContainers

		if _, err := p.CoreClient.Pods(p.ClusterNamespace).UpdateEphemeralContainers(ctx, hostPod.Name, &hostPod, metav1.UpdateOptions{}); err != nil {
			logger.Error(err, "Error when updating ephemeral containers in host pod")
			return err
		}
	}

	logger.Info("Pod updated in host cluster")

	//
	//	Virtual Pod update
	//

	key := types.NamespacedName{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}

	var virtualPod corev1.Pod
	if err := p.VirtualClient.Get(ctx, key, &virtualPod); err != nil {
		logger.Error(err, "Unable to get pod to update from virtual cluster")
		return err
	}

	updatePod(&virtualPod, pod)

	if err := p.VirtualClient.Update(ctx, &virtualPod); err != nil {
		logger.Error(err, "Unable to update Pod in virtual cluster")
		return err
	}

	// Ephemeral containers update (subresource)
	if !cmp.Equal(virtualPod.Spec.EphemeralContainers, pod.Spec.EphemeralContainers) {
		logger.V(1).Info("Updating ephemeral containers in virtual pod")

		virtualPod.Spec.EphemeralContainers = pod.Spec.EphemeralContainers

		if _, err := p.CoreClient.Pods(p.ClusterNamespace).UpdateEphemeralContainers(ctx, virtualPod.Name, &virtualPod, metav1.UpdateOptions{}); err != nil {
			logger.Error(err, "Error when updating ephemeral containers in virtual pod")
			return err
		}
	}

	logger.Info("Pod updated in virtual and host cluster")

	return nil
}

func updatePod(dst, src *corev1.Pod) {
	updateContainerImages(dst.Spec.Containers, src.Spec.Containers)
	updateContainerImages(dst.Spec.InitContainers, src.Spec.InitContainers)

	dst.Spec.ActiveDeadlineSeconds = src.Spec.ActiveDeadlineSeconds
	dst.Spec.Tolerations = src.Spec.Tolerations

	dst.Annotations = src.Annotations
	dst.Labels = src.Labels
}

// updateContainerImages will update the images of the original container images with the same name
func updateContainerImages(dst, src []corev1.Container) {
	images := make(map[string]string)

	for _, container := range src {
		images[container.Name] = container.Image
	}

	for i, container := range dst {
		dst[i].Image = images[container.Name]
	}
}

// DeletePod executes deletePod with retry
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	return p.withRetry(ctx, p.deletePod, pod)
}

// deletePod takes a Kubernetes Pod and deletes it from the provider. Once a pod is deleted, the provider is
// expected to call the NotifyPods callback with a terminal pod status where all the containers are in a terminal
// state, as well as the pod. DeletePod may be called multiple times for the same pod.
func (p *Provider) deletePod(ctx context.Context, pod *corev1.Pod) error {
	hostPodName := p.Translator.TranslateName(pod.Namespace, pod.Name)

	logger := p.logger.WithValues("namespace", pod.Namespace, "name", pod.Name, "pod", hostPodName)
	logger.V(1).Info("DeletePod")

	err := p.CoreClient.Pods(p.ClusterNamespace).Delete(ctx, hostPodName, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Pod to delete not found in host cluster")
			return nil
		}

		logger.Error(err, "Error trying to delete pod from host cluster")

		return err
	}

	logger.Info("Pod deleted from host cluster")

	return nil
}

// GetPod retrieves a pod by name from the provider (can be cached).
// The Pod returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	hostPodName := p.Translator.TranslateName(namespace, name)

	logger := p.logger.WithValues("namespace", namespace, "name", name, "pod", hostPodName)
	logger.V(1).Info("GetPod")

	pod, err := p.getPodFromHostCluster(ctx, hostPodName)
	if err != nil {
		logger.Error(err, "Error getting pod from host cluster for GetPod")
		return nil, err
	}

	return pod, nil
}

// GetPodStatus retrieves the status of a pod by name from the provider.
// The PodStatus returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	hostPodName := p.Translator.TranslateName(namespace, name)

	logger := p.logger.WithValues("namespace", namespace, "name", name, "pod", hostPodName)
	logger.V(1).Info("GetPodStatus")

	pod, err := p.getPodFromHostCluster(ctx, hostPodName)
	if err != nil {
		logger.Error(err, "Error getting pod from host cluster for PodStatus")
		return nil, err
	}

	return pod.Status.DeepCopy(), nil
}

func (p *Provider) getPodFromHostCluster(ctx context.Context, hostPodName string) (*corev1.Pod, error) {
	key := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      hostPodName,
	}

	var pod corev1.Pod
	if err := p.HostClient.Get(ctx, key, &pod); err != nil {
		return nil, err
	}

	p.Translator.TranslateFrom(&pod)

	return &pod, nil
}

// GetPods retrieves a list of all pods running on the provider (can be cached).
// The Pods returned are expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPods(ctx context.Context) ([]*corev1.Pod, error) {
	p.logger.V(1).Info("GetPods")

	selector := labels.NewSelector()

	requirement, err := labels.NewRequirement(translate.ClusterNameLabel, selection.Equals, []string{p.ClusterName})
	if err != nil {
		p.logger.Error(err, "Error creating label selector for GetPods")
		return nil, err
	}

	selector = selector.Add(*requirement)

	var podList corev1.PodList

	err = p.HostClient.List(ctx, &podList, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		p.logger.Error(err, "Error listing pods from host cluster")
		return nil, err
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
	if !strings.HasPrefix(podName, "coredns") && pod.Spec.DNSConfig == nil {
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
			Options: []corev1.PodDNSConfigOption{
				{
					Name:  "ndots",
					Value: ptr.To("5"),
				},
			},
		}
	}

	updatedEnvVars := []corev1.EnvVar{
		{Name: "KUBERNETES_SERVICE_HOST", Value: serverIP},
		{Name: "KUBERNETES_PORT", Value: "tcp://" + serverIP + ":443"},
		{Name: "KUBERNETES_PORT_443_TCP", Value: "tcp://" + serverIP + ":443"},
		{Name: "KUBERNETES_PORT_443_TCP_ADDR", Value: serverIP},
	}

	// inject networking information to the pod's environment variables
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].Env = mergeEnvVars(pod.Spec.Containers[i].Env, updatedEnvVars)
	}

	// handle init containers as well
	for i := range pod.Spec.InitContainers {
		pod.Spec.InitContainers[i].Env = mergeEnvVars(pod.Spec.InitContainers[i].Env, updatedEnvVars)
	}

	// handle ephemeral containers as well
	for i := range pod.Spec.EphemeralContainers {
		pod.Spec.EphemeralContainers[i].Env = mergeEnvVars(pod.Spec.EphemeralContainers[i].Env, updatedEnvVars)
	}
}

// mergeEnvVars will override the orig environment variables if found in the updated list and will add them to the list if not found
func mergeEnvVars(orig, updated []corev1.EnvVar) []corev1.EnvVar {
	if len(updated) == 0 {
		return orig
	}

	// create map for single lookup
	updatedEnvVarMap := make(map[string]corev1.EnvVar)
	for _, updatedEnvVar := range updated {
		updatedEnvVarMap[updatedEnvVar.Name] = updatedEnvVar
	}

	for i, env := range orig {
		if updatedEnv, ok := updatedEnvVarMap[env.Name]; ok {
			orig[i] = updatedEnv
			// Remove the updated variable from the map
			delete(updatedEnvVarMap, env.Name)
		}
	}

	// Any variables remaining in the map are new and should be appended to the original slice.
	for _, env := range updatedEnvVarMap {
		orig = append(orig, env)
	}

	return orig
}

func configurePodEnvs(hostPod, virtualPod *corev1.Pod) {
	for i := range hostPod.Spec.Containers {
		// todo check container name
		hostPod.Spec.Containers[i].Env = configureEnv(virtualPod, virtualPod.Spec.Containers[i].Env)
	}

	for i := range hostPod.Spec.InitContainers {
		// todo check container name
		hostPod.Spec.InitContainers[i].Env = configureEnv(virtualPod, virtualPod.Spec.InitContainers[i].Env)
	}

	for i := range hostPod.Spec.EphemeralContainers {
		// todo check container name
		hostPod.Spec.EphemeralContainers[i].Env = configureEnv(virtualPod, virtualPod.Spec.EphemeralContainers[i].Env)
	}
}

func configureEnv(virtualPod *corev1.Pod, envs []corev1.EnvVar) []corev1.EnvVar {
	resultingEnvVars := make([]corev1.EnvVar, 0, len(envs))

	for _, envVar := range envs {
		resultingEnvVar := envVar

		if envVar.ValueFrom != nil {
			if envVar.ValueFrom.FieldRef != nil {
				fieldRef := envVar.ValueFrom.FieldRef

				// for name and namespace we need to hardcode the virtual cluster values, and clear the FieldRef
				switch fieldRef.FieldPath {
				case "metadata.name":
					resultingEnvVar.Value = virtualPod.Name
					resultingEnvVar.ValueFrom = nil
				case "metadata.namespace":
					resultingEnvVar.Value = virtualPod.Namespace
					resultingEnvVar.ValueFrom = nil
				}
			}

			// TODO we need to handle also ConfigMaps and Secrets
		}

		resultingEnvVars = append(resultingEnvVars, resultingEnvVar)
	}

	return resultingEnvVars
}

// configureFieldPathEnv will retrieve all annotations created by the pod mutating webhook
// to assign env fieldpaths to pods, it will also make sure to change the metadata.name and metadata.namespace to the
// assigned annotations
func (p *Provider) configureFieldPathEnv(pod, tPod *corev1.Pod) error {
	for name, value := range pod.Annotations {
		if strings.Contains(name, webhook.FieldpathField) {
			containerIndex, envName, err := webhook.ParseFieldPathAnnotationKey(name)
			if err != nil {
				return err
			}
			// re-adding these envs to the pod
			tPod.Spec.Containers[containerIndex].Env = append(tPod.Spec.Containers[containerIndex].Env, corev1.EnvVar{
				Name: envName,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
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

// func printPod(pod corev1.Pod) {
// 	resK, _ := json.MarshalIndent(pod, "", "  ")

// 	fmt.Printf("\n### Pod %s/%s\n\n%#v\n\n", pod.Namespace, pod.Name, string(resK))
// }
