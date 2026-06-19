// Package portforward provides a thin test-only helper around
// k8s.io/client-go/tools/portforward to tunnel a local TCP port into a Service
// in the host cluster. It exists so e2e tests can talk to a virtual cluster's
// API without relying on how that cluster is exposed (NodePort/LoadBalancer/
// Ingress), which differs between k3d, RKE2-behind-NLB, EKS, etc.
package portforward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const readyTimeout = 30 * time.Second

// Forwarder is a live port-forward tunnel. LocalPort is the address-loopback
// port the kernel picked; Stop tears down the tunnel and releases the port.
type Forwarder struct {
	LocalPort int
	Stop      func()
}

// ToService finds the first Running pod backing the named Service and
// port-forwards a free local port to that pod's service TargetPort.
// It blocks until the forward is ready.
func ToService(ctx context.Context, restConfig *rest.Config, client kubernetes.Interface, namespace, serviceName string) (*Forwarder, error) {
	svc, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get service %s/%s: %w", namespace, serviceName, err)
	}

	if len(svc.Spec.Ports) == 0 {
		return nil, fmt.Errorf("service %s/%s has no ports", namespace, serviceName)
	}

	targetPort := svc.Spec.Ports[0].TargetPort.IntValue()
	if targetPort == 0 {
		// String target ports are not addressable by port-forward; fall back to Port.
		targetPort = int(svc.Spec.Ports[0].Port)
	}

	pod, err := firstRunningPod(ctx, client, namespace, svc.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("find pod for service %s/%s: %w", namespace, serviceName, err)
	}

	return ToPod(restConfig, client, namespace, pod.Name, targetPort)
}

// ToPod port-forwards a free local port to portInPod inside the named pod.
func ToPod(restConfig *rest.Config, client kubernetes.Interface, namespace, podName string, portInPod int) (*Forwarder, error) {
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("spdy round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())

	stop := make(chan struct{})
	ready := make(chan struct{})
	done := make(chan struct{})
	errCh := make(chan error, 1)

	ports := []string{"0:" + strconv.Itoa(portInPod)}

	fw, err := portforward.New(dialer, ports, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("create port-forward: %w", err)
	}

	go func() {
		defer close(done)

		if err := fw.ForwardPorts(); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ready:
	case err := <-errCh:
		return nil, fmt.Errorf("port-forward failed before ready: %w", err)
	case <-time.After(readyTimeout):
		close(stop)
		<-done

		return nil, fmt.Errorf("port-forward to %s/%s not ready after %s", namespace, podName, readyTimeout)
	}

	forwarded, err := fw.GetPorts()
	if err != nil {
		close(stop)
		<-done

		return nil, fmt.Errorf("read forwarded ports: %w", err)
	}

	if len(forwarded) == 0 {
		close(stop)
		<-done

		return nil, errors.New("port-forward returned no ports")
	}

	stopOnce := make(chan struct{})

	return &Forwarder{
		LocalPort: int(forwarded[0].Local),
		Stop: func() {
			select {
			case <-stopOnce:
				return
			default:
				close(stopOnce)
			}

			close(stop)
			<-done
		},
	}, nil
}

func firstRunningPod(ctx context.Context, client kubernetes.Interface, namespace string, selector map[string]string) (*corev1.Pod, error) {
	if len(selector) == 0 {
		return nil, errors.New("service has no selector")
	}

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
