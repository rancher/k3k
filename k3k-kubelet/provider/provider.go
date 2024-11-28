package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/rancher/k3k/k3k-kubelet/controller"
	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3klog "github.com/rancher/k3k/pkg/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	cv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	metricset "k8s.io/metrics/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// translatorSizeQueue feeds the size events from the WebSocket
// resizeChan into the SPDY client input. Implements TerminalSizeQueue
// interface.
type translatorSizeQueue struct {
	resizeChan <-chan api.TermSize
}

func (t *translatorSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-t.resizeChan
	if !ok {
		return nil
	}
	newSize := remotecommand.TerminalSize{
		Width:  size.Width,
		Height: size.Height,
	}

	return &newSize
}

// Provider implements nodetuil.Provider from virtual Kubelet.
type Provider struct {
	Handler          controller.ControllerHandler
	Translater       translate.ToHostTranslater
	HostClient       client.Client
	VirtualClient    client.Client
	ClientConfig     rest.Config
	CoreClient       cv1.CoreV1Interface
	MetricsClient    metricset.Interface
	ClusterNamespace string
	ClusterName      string
	logger           *k3klog.Logger
}

func New(hostConfig rest.Config, hostMgr, virtualMgr manager.Manager, logger *k3klog.Logger, Namespace, Name string) (*Provider, error) {
	coreClient, err := cv1.NewForConfig(&hostConfig)
	if err != nil {
		return nil, err
	}

	translater := translate.ToHostTranslater{
		ClusterName:      Name,
		ClusterNamespace: Namespace,
	}

	return &Provider{
		Handler: controller.ControllerHandler{
			Mgr:           virtualMgr,
			Scheme:        *virtualMgr.GetScheme(),
			HostClient:    hostMgr.GetClient(),
			VirtualClient: virtualMgr.GetClient(),
			Translater:    translater,
			Logger:        logger,
		},
		HostClient:       hostMgr.GetClient(),
		VirtualClient:    virtualMgr.GetClient(),
		Translater:       translater,
		ClientConfig:     hostConfig,
		CoreClient:       coreClient,
		ClusterNamespace: Namespace,
		ClusterName:      Name,
		logger:           logger,
	}, nil
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	hostPodName := p.Translater.TranslateName(namespace, podName)
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
		p.logger.Infof("error %s when getting logs for %s in %s", err, hostPodName, p.ClusterNamespace)
		return closer, err
	}

	return closer, err
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(ctx context.Context, namespace, podName, containerName string, cmd []string, attach api.AttachIO) error {
	hostPodName := p.Translater.TranslateName(namespace, podName)
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
	hostPodName := p.Translater.TranslateName(namespace, podName)
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
func (p *Provider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	return nil, errors.New("not implemented")
}

// GetMetricsResource gets the metrics for the node, including running pods
func (p *Provider) GetMetricsResource(context.Context) ([]*dto.MetricFamily, error) {
	return nil, errors.New("not implemented")
}

// PortForward forwards a local port to a port on the pod
func (p *Provider) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	hostPodName := p.Translater.TranslateName(namespace, pod)
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

// CreatePod takes a Kubernetes Pod and deploys it within the provider.
func (p *Provider) CreatePod(ctx context.Context, pod *corev1.Pod) error {
	tPod := pod.DeepCopy()
	p.Translater.TranslateTo(tPod)

	// get Cluster definition
	clusterKey := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      p.ClusterName,
	}
	var cluster v1alpha1.Cluster
	if err := p.HostClient.Get(ctx, clusterKey, &cluster); err != nil {
		return err
	}

	// these values shouldn't be set on create
	tPod.UID = ""
	tPod.ResourceVersion = ""

	// the node was scheduled on the virtual kubelet, but leaving it this way will make it pending indefinitely
	tPod.Spec.NodeName = ""

	tPod.Spec.NodeSelector = cluster.Spec.NodeSelector

	// volumes will often refer to resources in the virtual cluster, but instead need to refer to the sync'd
	// host cluster version
	if err := p.transformVolumes(ctx, pod.Namespace, tPod.Spec.Volumes); err != nil {
		return err
	}

	// sync serviceaccount token to a the host cluster
	if err := p.transformTokens(ctx, pod, tPod); err != nil {
		return err
	}

	p.logger.Infow("Creating pod", "Host Namespace", tPod.Namespace, "Host Name", tPod.Name,
		"Virtual Namespace", pod.Namespace, "Virtual Name", pod.Name)

	return p.HostClient.Create(ctx, tPod)
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
				return err
			}

			volume.ConfigMap.Name = p.Translater.TranslateName(podNamespace, volume.ConfigMap.Name)
		} else if volume.Secret != nil {
			if volume.Secret.Optional != nil {
				optional = *volume.Secret.Optional
			}
			if err := p.syncSecret(ctx, podNamespace, volume.Secret.SecretName, optional); err != nil {
				return err
			}

			volume.Secret.SecretName = p.Translater.TranslateName(podNamespace, volume.Secret.SecretName)
		} else if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap != nil {
					if source.ConfigMap.Optional != nil {
						optional = *source.ConfigMap.Optional
					}
					configMapName := source.ConfigMap.Name
					if err := p.syncConfigmap(ctx, podNamespace, configMapName, optional); err != nil {
						return err
					}

					source.ConfigMap.Name = p.Translater.TranslateName(podNamespace, configMapName)
				} else if source.Secret != nil {
					if source.Secret.Optional != nil {
						optional = *source.Secret.Optional
					}

					secretName := source.Secret.Name
					if err := p.syncSecret(ctx, podNamespace, secretName, optional); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

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
		return err
	}
	if err := p.Handler.AddResource(ctx, &configMap); err != nil {
		return err
	}

	return nil
}

func (p *Provider) syncSecret(ctx context.Context, podNamespace string, secretName string, optional bool) error {
	var secret corev1.Secret

	nsName := types.NamespacedName{
		Namespace: podNamespace,
		Name:      secretName,
	}
	if err := p.VirtualClient.Get(ctx, nsName, &secret); err != nil {
		if apierrors.IsNotFound(err) && optional {
			return nil
		}
		return err
	}

	return p.Handler.AddResource(ctx, &secret)
}

// UpdatePod takes a Pod and updates it within the provider.
func (p *Provider) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	hostName := p.Translater.TranslateName(pod.Namespace, pod.Name)
	currentPod, err := p.GetPod(ctx, p.ClusterNamespace, hostName)
	if err != nil {
		return err
	}

	tPod := pod.DeepCopy()
	p.Translater.TranslateTo(tPod)
	tPod.UID = currentPod.UID
	// this is a bit dangerous since another process could have made changes that the user didn't know about
	tPod.ResourceVersion = currentPod.ResourceVersion

	// Volumes may refer to resources (configmaps/secrets) from the host cluster
	// So we need the configuration as calculated during create time
	tPod.Spec.Volumes = currentPod.Spec.Volumes
	tPod.Spec.Containers = currentPod.Spec.Containers
	tPod.Spec.InitContainers = currentPod.Spec.InitContainers
	tPod.Spec.NodeName = currentPod.Spec.NodeName

	return p.HostClient.Update(ctx, tPod)
}

// DeletePod takes a Kubernetes Pod and deletes it from the provider. Once a pod is deleted, the provider is
// expected to call the NotifyPods callback with a terminal pod status where all the containers are in a terminal
// state, as well as the pod. DeletePod may be called multiple times for the same pod.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	p.logger.Infof("Got request to delete pod %s", pod.Name)

	hostName := p.Translater.TranslateName(pod.Namespace, pod.Name)
	if err := p.CoreClient.Pods(p.ClusterNamespace).Delete(ctx, hostName, metav1.DeleteOptions{}); err != nil {
		return err
	}

	if err := p.pruneUnusedVolumes(ctx, pod); err != nil {
		// note that we don't return an error here. The pod was sucessfully deleted, another process
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
	if err := p.VirtualClient.List(ctx, &pods, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
		return err
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
		if err := p.VirtualClient.Get(ctx, types.NamespacedName{
			Name:      secretName,
			Namespace: pod.Namespace,
		}, &secret); err != nil {
			return err
		}

		if err := p.Handler.RemoveResource(ctx, &secret); err != nil {
			return err
		}
	}

	for _, configMapName := range pruneConfigMap.UnsortedList() {
		var configMap corev1.ConfigMap
		err := p.VirtualClient.Get(ctx, types.NamespacedName{
			Name:      configMapName,
			Namespace: pod.Namespace,
		}, &configMap)
		if err != nil {
			return err
		}

		if err = p.Handler.RemoveResource(ctx, &configMap); err != nil {
			return err
		}
	}

	return nil
}

// GetPod retrieves a pod by name from the provider (can be cached).
// The Pod returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	p.logger.Infow("got a request for get pod", "Namespace", namespace, "Name", name)
	hostNamespaceName := types.NamespacedName{
		Namespace: p.ClusterNamespace,
		Name:      p.Translater.TranslateName(namespace, name),
	}

	var pod corev1.Pod
	if err := p.HostClient.Get(ctx, hostNamespaceName, &pod); err != nil {
		return nil, err
	}

	p.Translater.TranslateFrom(&pod)

	return &pod, nil
}

// GetPodStatus retrieves the status of a pod by name from the provider.
// The PodStatus returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	p.logger.Infow("got a request for pod status", "Namespace", namespace, "Name", name)

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	selector = selector.Add(*requirement)

	var podList corev1.PodList
	if err = p.HostClient.List(ctx, &podList, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}

	len := len(podList.DeepCopy().Items)
	retPods := make([]*corev1.Pod, len)
	for _, pod := range podList.DeepCopy().Items {
		p.Translater.TranslateFrom(&pod)
		retPods = append(retPods, &pod)
	}

	return retPods, nil
}

// getSecretsAndConfigmaps retrieves a list of all secrets/configmaps that are in use by a given pod. Useful
// for removing/seeing which virtual cluster resources need to be in the host cluster.
func getSecretsAndConfigmaps(pod *corev1.Pod) ([]string, []string) {
	var secrets []string
	var configMaps []string

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
