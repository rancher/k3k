// Package portforward provides a test-only TCP proxy that tunnels each
// accepted connection through a fresh SPDY port-forward stream to a freshly
// looked-up pod backing a host-cluster Service. It exists so e2e tests can
// talk to a virtual cluster's API without relying on how that cluster is
// exposed (NodePort/LoadBalancer/Ingress), which differs between k3d,
// RKE2-behind-NLB, EKS, etc.
//
// Per-connection isolation matters: a single SPDY tunnel with many multiplexed
// streams (e.g. exec-heavy specs) tears down everything on one stream error,
// and pins the tunnel to one pod (broken on server restart). Re-resolving the
// backing pod and opening a fresh SPDY connection per accepted TCP connection
// avoids both failure modes.
package portforward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport/spdy"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const portForwardProtocol = "portforward.k8s.io"

// Forwarder is a TCP listener that proxies each accepted connection to a
// freshly looked-up pod backing the configured Service.
type Forwarder struct {
	LocalPort int
	Stop      func()
}

// ToService returns a Forwarder bound to 127.0.0.1 on a kernel-picked port.
// Each accepted connection is independently tunneled to a Running pod backing
// the named Service via its own SPDY stream.
func ToService(ctx context.Context, restConfig *rest.Config, client kubernetes.Interface, namespace, serviceName string) (*Forwarder, error) {
	svc, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get service %s/%s: %w", namespace, serviceName, err)
	}

	if len(svc.Spec.Ports) == 0 {
		return nil, fmt.Errorf("service %s/%s has no ports", namespace, serviceName)
	}

	if len(svc.Spec.Selector) == 0 {
		return nil, fmt.Errorf("service %s/%s has no selector", namespace, serviceName)
	}

	targetPort := svc.Spec.Ports[0].TargetPort.IntValue()
	if targetPort == 0 {
		// Named target ports aren't addressable by portforward; fall back to Port.
		targetPort = int(svc.Spec.Ports[0].Port)
	}

	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("spdy round tripper: %w", err)
	}

	httpClient := &http.Client{Transport: transport}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	localPort := listener.Addr().(*net.TCPAddr).Port

	connCtx, cancel := context.WithCancel(context.Background())

	var (
		stopOnce sync.Once
		wg       sync.WaitGroup
	)

	stop := func() {
		stopOnce.Do(func() {
			cancel()
			_ = listener.Close()
		})

		wg.Wait()
	}

	go acceptLoop(connCtx, listener, &wg, client, httpClient, upgrader, namespace, svc.Spec.Selector, targetPort)

	return &Forwarder{LocalPort: localPort, Stop: stop}, nil
}

func acceptLoop(ctx context.Context, listener net.Listener, wg *sync.WaitGroup, client kubernetes.Interface, httpClient *http.Client, upgrader spdy.Upgrader, namespace string, selector map[string]string, targetPort int) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Listener closed via Stop(); exit cleanly.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Otherwise the listener is probably broken; nothing useful we can do.
			return
		}

		wg.Add(1)

		go func() {
			defer wg.Done()
			handleConnection(ctx, conn, client, httpClient, upgrader, namespace, selector, targetPort)
		}()
	}
}

// requestID is a process-wide counter for portforward request IDs.
var requestID uint64

func handleConnection(ctx context.Context, conn net.Conn, client kubernetes.Interface, httpClient *http.Client, upgrader spdy.Upgrader, namespace string, selector map[string]string, targetPort int) {
	defer conn.Close()

	pod, err := findRunningPod(ctx, client, namespace, selector)
	if err != nil {
		return
	}

	pfURL := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(pod.Name).
		SubResource("portforward").
		URL()

	dialer := spdy.NewDialer(upgrader, httpClient, http.MethodPost, pfURL)

	streamConn, _, err := dialer.Dial(portForwardProtocol + "/v1")
	if err != nil {
		return
	}
	defer streamConn.Close()

	rid := strconv.FormatUint(atomic.AddUint64(&requestID, 1), 10)

	headers := http.Header{}
	headers.Set(corev1.PortHeader, strconv.Itoa(targetPort))
	headers.Set(corev1.PortForwardRequestIDHeader, rid)

	// The error stream is required by the protocol; we don't write to it.
	headers.Set(corev1.StreamType, corev1.StreamTypeError)

	errStream, err := streamConn.CreateStream(headers)
	if err != nil {
		return
	}
	_ = errStream.Close()

	defer streamConn.RemoveStreams(errStream)

	headers.Set(corev1.StreamType, corev1.StreamTypeData)

	dataStream, err := streamConn.CreateStream(headers)
	if err != nil {
		return
	}
	defer streamConn.RemoveStreams(dataStream)

	// Bidirectional copy. Either direction finishing tears down both sides.
	remoteDone := make(chan struct{})
	localDone := make(chan struct{})

	go func() {
		defer close(remoteDone)

		_, _ = io.Copy(conn, dataStream)
	}()

	go func() {
		defer close(localDone)
		defer dataStream.Close()

		_, _ = io.Copy(dataStream, conn)
	}()

	select {
	case <-remoteDone:
	case <-localDone:
	}
}

func findRunningPod(ctx context.Context, client kubernetes.Interface, namespace string, selector map[string]string) (*corev1.Pod, error) {
	labelSelector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: selector})

	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}

	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
			return p, nil
		}
	}

	return nil, fmt.Errorf("no running pod for selector %q", labelSelector)
}
