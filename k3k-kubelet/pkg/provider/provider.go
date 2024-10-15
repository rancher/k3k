package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	cv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	metricset "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	clusterNameLabel       = "virt.cattle.io/clusterName"
	podNameAnnotation      = "virt.cattle.io/podName"
	podNamespaceAnnotation = "virt.cattle.io/podNamespace"
)

// Provider implements nodetuil.Provider from virtual Kubelet.
// TODO: Implement NotifyPods and the required usage so that this can be an async provider
type Provider struct {
	ClientConfig     rest.Config
	CoreClient       cv1.CoreV1Interface
	MetricsClient    metricset.Interface
	ClusterNamespace string
	ClusterName      string
	logger           zap.SugaredLogger
}

func New(config rest.Config, Namespace string, Name string) (*Provider, error) {
	coreClient, err := cv1.NewForConfig(&config)
	if err != nil {
		return nil, err
	}
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}
	p := Provider{
		ClientConfig:     config,
		CoreClient:       coreClient,
		ClusterNamespace: Namespace,
		ClusterName:      Name,
		logger:           *logger.Sugar(),
	}

	return &p, nil
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *Provider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	hostPodName := p.hostName(namespace, podName)
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
	req := p.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(p.hostName(namespace, podName)).
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
	req := p.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(p.hostName(namespace, podName)).
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
	return nil, fmt.Errorf("not implemented")
}

// GetMetricsResource gets the metrics for the node, including running pods
func (p *Provider) GetMetricsResource(context.Context) ([]*dto.MetricFamily, error) {
	return nil, fmt.Errorf("not implemented")
}

// PortForward forwards a local port to a port on the pod
func (p *Provider) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	req := p.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(p.hostName(namespace, pod)).
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
	translated := p.translateTo(pod)
	// these values shouldn't be set on create
	translated.UID = ""
	translated.ResourceVersion = ""
	// can't handle volumes yet, just omit them for now
	translated.Spec.Volumes = nil
	containers := []corev1.Container{}
	for _, container := range translated.Spec.Containers {
		container.VolumeMounts = nil
		containers = append(containers, container)
	}
	translated.Spec.Containers = containers
	translated.Spec.NodeName = ""
	p.logger.Infof("Creating pod %s/%s for pod %s/%s", translated.Namespace, translated.Name, pod.Namespace, pod.Name)
	_, err := p.CoreClient.Pods(p.ClusterNamespace).Create(ctx, translated, metav1.CreateOptions{})
	return err
}

// UpdatePod takes a Kubernetes Pod and updates it within the provider.
func (p *Provider) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	currentPod, err := p.GetPod(ctx, p.ClusterNamespace, p.hostName(p.ClusterNamespace, p.ClusterName))
	if err != nil {
		return err
	}
	translated := p.translateTo(pod)
	translated.UID = currentPod.UID
	translated.ResourceVersion = currentPod.ResourceVersion
	containers := []corev1.Container{}
	for _, container := range translated.Spec.Containers {
		container.VolumeMounts = nil
		containers = append(containers, container)
	}
	translated.Spec.Containers = containers
	translated.Spec.NodeName = currentPod.Spec.NodeName

	_, err = p.CoreClient.Pods(p.ClusterNamespace).Update(ctx, translated, metav1.UpdateOptions{})
	return err
}

// DeletePod takes a Kubernetes Pod and deletes it from the provider. Once a pod is deleted, the provider is
// expected to call the NotifyPods callback with a terminal pod status where all the containers are in a terminal
// state, as well as the pod. DeletePod may be called multiple times for the same pod.
func (p *Provider) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	err := p.CoreClient.Pods(p.ClusterNamespace).Delete(ctx, p.translateTo(pod).Name, metav1.DeleteOptions{})
	p.logger.Infof("Deleted pod %s", pod.Name)
	return err
}

// GetPod retrieves a pod by name from the provider (can be cached).
// The Pod returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	pod, err := p.CoreClient.Pods(p.ClusterNamespace).Get(ctx, p.hostName(namespace, name), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return p.translateFrom(pod), nil
}

// GetPodStatus retrieves the status of a pod by name from the provider.
// The PodStatus returned is expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPodStatus(ctx context.Context, namespace, name string) (*corev1.PodStatus, error) {
	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	return pod.Status.DeepCopy(), nil
}

// GetPods retrieves a list of all pods running on the provider (can be cached).
// The Pods returned are expected to be immutable, and may be accessed
// concurrently outside of the calling goroutine. Therefore it is recommended
// to return a version after DeepCopy.
func (p *Provider) GetPods(ctx context.Context) ([]*corev1.Pod, error) {
	labelSelector := metav1.AddLabelToSelector(&metav1.LabelSelector{}, clusterNameLabel, p.ClusterName)
	pods, err := p.CoreClient.Pods(p.ClusterNamespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector.String()})
	if err != nil {
		return nil, err
	}
	retPods := []*corev1.Pod{}
	for _, pod := range pods.DeepCopy().Items {
		retPods = append(retPods, p.translateFrom(&pod))
	}
	return retPods, nil
}

// translateTo translates a pod's definition from the virutal cluster to the definition on the host cluster
func (p *Provider) translateTo(virtualPod *corev1.Pod) *corev1.Pod {
	hostPod := virtualPod.DeepCopy()
	hostPod.Name = p.hostName(virtualPod.Namespace, virtualPod.Name)
	hostPod.Namespace = p.ClusterNamespace
	if hostPod.Labels == nil {
		hostPod.Labels = map[string]string{}
	}
	if hostPod.Annotations == nil {
		hostPod.Annotations = map[string]string{}
	}
	hostPod.Labels[clusterNameLabel] = p.ClusterName
	hostPod.Annotations[podNameAnnotation] = virtualPod.Name
	hostPod.Annotations[podNamespaceAnnotation] = virtualPod.Namespace
	return hostPod
}

// translateFrom translates a pod's definition from the host cluster to the definition on the host cluster
func (p *Provider) translateFrom(hostPod *corev1.Pod) *corev1.Pod {
	virtualPod := hostPod.DeepCopy()
	delete(virtualPod.Labels, clusterNameLabel)
	virtualPod.Name = hostPod.Annotations[podNameAnnotation]
	virtualPod.Namespace = hostPod.Annotations[podNamespaceAnnotation]
	delete(virtualPod.Annotations, podNameAnnotation)
	delete(virtualPod.Annotations, podNamespaceAnnotation)
	return virtualPod
}

func (p *Provider) hostName(virtualNamespace string, virtualName string) string {
	return safeConcatName(p.ClusterName, p.ClusterNamespace, virtualNamespace, virtualName)
}

// safeConcatName concatenates the given strings and ensures the returned name is under 64 characters
// by cutting the string off at 57 characters and setting the last 6 with an encoded version of the concatenated string.
func safeConcatName(name ...string) string {
	fullPath := strings.Join(name, "-")
	if len(fullPath) < 64 {
		return fullPath
	}
	digest := sha256.Sum256([]byte(fullPath))
	// since we cut the string in the middle, the last char may not be compatible with what is expected in k8s
	// we are checking and if necessary removing the last char
	c := fullPath[56]
	if 'a' <= c && c <= 'z' || '0' <= c && c <= '9' {
		return fullPath[0:57] + "-" + hex.EncodeToString(digest[0:])[0:5]
	}

	return fullPath[0:56] + "-" + hex.EncodeToString(digest[0:])[0:6]
}
