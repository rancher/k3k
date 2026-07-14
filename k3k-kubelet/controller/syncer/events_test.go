package syncer

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

func TestEventSyncerReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	hostPod := func(name, namespace, virtualName, virtualNamespace string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Annotations: map[string]string{
					translate.ResourceNameAnnotation:      virtualName,
					translate.ResourceNamespaceAnnotation: virtualNamespace,
				},
				UID:             types.UID("pod-host-uid"),
				ResourceVersion: "1",
			},
		}
	}

	virtualPod := func(name, namespace string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       namespace,
				UID:             types.UID("pod-virtual-uid"),
				ResourceVersion: "1",
			},
		}
	}

	tests := map[string]struct {
		// the event being reconciled
		receivedEvent *corev1.Event

		hostObj    *corev1.Pod
		virtualObj *corev1.Pod
		wantEvent  *capturedEvent
	}{
		"normal event is re-emitted via virtEventRecorder": {
			receivedEvent: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572.18b194827e6b49da",
					Namespace: "k3k-mycluster",
				},
				InvolvedObject: corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572",
					Namespace:  "k3k-mycluster",
				},
				Type:    corev1.EventTypeNormal,
				Reason:  "Started",
				Message: "Container started successfully",
			},
			hostObj:    hostPod("nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572", "k3k-mycluster", "nginx", "test"),
			virtualObj: virtualPod("nginx", "test"),
			wantEvent: &capturedEvent{
				Object:    virtualPod("nginx", "test"),
				EventType: corev1.EventTypeNormal,
				Reason:    "Started",
				Message:   "Container started successfully",
			},
		},
		"warning event is re-emitted via virtEventRecorder": {
			receivedEvent: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-event-warning",
					Namespace: "host-ns",
				},
				InvolvedObject: corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572",
					Namespace:  "k3k-mycluster",
				},
				Type:    corev1.EventTypeWarning,
				Reason:  "BackOff",
				Message: "Back-off restarting failed container",
			},
			hostObj:    hostPod("nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572", "k3k-mycluster", "nginx", "test"),
			virtualObj: virtualPod("nginx", "test"),
			wantEvent: &capturedEvent{
				Object:    virtualPod("nginx", "test"),
				EventType: corev1.EventTypeWarning,
				Reason:    "BackOff",
				Message:   "Back-off restarting failed container",
			},
		},
		"event with empty type and reason is still forwarded": {
			receivedEvent: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-event-empty",
					Namespace: "host-ns",
				},
				InvolvedObject: corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572",
					Namespace:  "k3k-mycluster",
				},
				Type:    "",
				Reason:  "",
				Message: "some message",
			},
			hostObj:    hostPod("nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572", "k3k-mycluster", "nginx", "test"),
			virtualObj: virtualPod("nginx", "test"),
			wantEvent: &capturedEvent{
				Object:    virtualPod("nginx", "test"),
				EventType: "",
				Reason:    "",
				Message:   "some message",
			},
		},
		"message containing host namespace/name is translated to virtual name/namespace": {
			receivedEvent: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572.18b194827e6b49da",
					Namespace: "k3k-mycluster",
				},
				InvolvedObject: corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572",
					Namespace:  "k3k-mycluster",
				},
				Type:    corev1.EventTypeNormal,
				Reason:  "Scheduled",
				Message: "Successfully assigned k3k-mycluster/nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572 to localhost.localdomain",
			},
			hostObj:    hostPod("nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572", "k3k-mycluster", "nginx", "test"),
			virtualObj: virtualPod("nginx", "test"),
			wantEvent: &capturedEvent{
				Object:    virtualPod("nginx", "test"),
				EventType: corev1.EventTypeNormal,
				Reason:    "Scheduled",
				Message:   "Successfully assigned test/nginx to localhost.localdomain",
			},
		},
		"non-Pod InvolvedObject kind skips reconciliation": {
			receivedEvent: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-event-non-pod",
					Namespace: "k3k-mycluster",
				},
				InvolvedObject: corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "nginx-test-mycluster-6e67696e782b746573742b6d79636c7573746572",
					Namespace:  "k3k-mycluster",
				},
				Type:    corev1.EventTypeNormal,
				Reason:  "Updated",
				Message: "ConfigMap updated",
			},
			// wantEvent is nil: non-Pod InvolvedObject is skipped
			wantEvent: nil,
		},
		"non-reversible translated name skips event emission": {
			receivedEvent: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-event-non-reversible",
					Namespace: "k3k-mycluster",
				},
				InvolvedObject: corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "some-non-translated-name",
					Namespace:  "k3k-mycluster",
				},
				Type:    corev1.EventTypeNormal,
				Reason:  "Started",
				Message: "Container started",
			},
			hostObj: hostPod("some-non-translated-name", "k3k-mycluster", "", ""),
			// virtualObj is nil: object not found in virtual cluster → event is skipped
			wantEvent: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			hostFakeObjs := []client.Object{tt.receivedEvent}
			if tt.hostObj != nil {
				hostFakeObjs = append(hostFakeObjs, tt.hostObj)
			}

			hostFakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hostFakeObjs...).
				Build()

			virtFakeObjs := []client.Object{}
			if tt.virtualObj != nil {
				virtFakeObjs = append(virtFakeObjs, tt.virtualObj)
			}

			virtualFakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(virtFakeObjs...).
				Build()

			recorder := &fakeEventRecorder{}

			syncer := &EventSyncer{
				virtEventRecorder: recorder,
				SyncerContext: &SyncerContext{
					HostClient:    hostFakeClient,
					VirtualClient: virtualFakeClient,
					Translator: translate.ToHostTranslator{
						ClusterName:      "mycluster",
						ClusterNamespace: "k3k-mycluster",
					},
					ClusterName:      "mycluster",
					ClusterNamespace: "k3k-mycluster",
				},
			}

			result, err := syncer.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.receivedEvent.Name,
					Namespace: tt.receivedEvent.Namespace,
				},
			})

			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, result)

			if tt.wantEvent == nil {
				assert.Empty(t, recorder.events)
			} else {
				require.Len(t, recorder.events, 1)
				got := recorder.events[0]
				assert.Equal(t, tt.wantEvent.EventType, got.EventType)
				assert.Equal(t, tt.wantEvent.Reason, got.Reason)
				assert.Equal(t, tt.wantEvent.Message, got.Message)
				wantObj := tt.wantEvent.Object.(*corev1.Pod)
				gotObj := got.Object.(*corev1.Pod)
				assert.Equal(t, wantObj.GetName(), gotObj.GetName())
				assert.Equal(t, wantObj.GetNamespace(), gotObj.GetNamespace())
				assert.Equal(t, wantObj.GetUID(), gotObj.GetUID())
			}
		})
	}
}

func TestEventSyncerReconcileNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	recorder := &fakeEventRecorder{}

	syncer := &EventSyncer{
		virtEventRecorder: recorder,
		SyncerContext: &SyncerContext{
			HostClient:    fakeClient,
			VirtualClient: fakeClient,
			Translator: translate.ToHostTranslator{
				ClusterName:      "mycluster",
				ClusterNamespace: "host-ns",
			},
			ClusterName:      "mycluster",
			ClusterNamespace: "host-ns",
		},
	}

	result, err := syncer.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "non-existent-event",
			Namespace: "host-ns",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)

	assert.Empty(t, recorder.events)
}

// This fake is used instead of the one in the client-go package because we need to check that the events are sent to the correct namespace.
//
// The client-go fake doesn't support namespaces, so we implement our own that captures the events in memory for verification.
type capturedEvent struct {
	Object    runtime.Object
	EventType string
	Reason    string
	Message   string
}

type fakeEventRecorder struct {
	events []capturedEvent
}

func (f *fakeEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	f.events = append(f.events, capturedEvent{
		Object:    object,
		EventType: eventtype,
		Reason:    reason,
		Message:   message,
	})
}

func (f *fakeEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...any) {
	f.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

func (f *fakeEventRecorder) AnnotatedEventf(object runtime.Object, _ map[string]string, eventtype, reason, messageFmt string, args ...any) {
	f.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}
