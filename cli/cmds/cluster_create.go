package cmds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

// CreateConfig holds the flags of the "cluster create" command.
type CreateConfig struct {
	kubeconfigOutFlags

	token                string
	clusterCIDR          string
	serviceCIDR          string
	servers              int
	agents               int
	serverArgs           []string
	agentArgs            []string
	serverEnvs           []string
	agentEnvs            []string
	labels               []string
	annotations          []string
	tlsSANs              []string
	persistenceType      string
	storageClassName     string
	storageRequestSize   string
	version              string
	mode                 string
	kubeconfigServerHost string
	policy               string
	mirrorHostNodes      bool
	customCertsPath      string
	timeout              time.Duration
}

// NewClusterCreateCmd returns the "cluster create" command.
func NewClusterCreateCmd(appCtx *AppContext) *cobra.Command {
	createConfig := &CreateConfig{}

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a new cluster.",
		Example: "k3kcli cluster create [command options] NAME",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return validateCreateConfig(createConfig)
		},
		RunE: createAction(appCtx, createConfig),
		Args: cobra.ExactArgs(1),
	}

	createFlags(cmd, createConfig)

	CobraFlagNamespace(appCtx, cmd, completeNamespaces)

	CobraFlagKubeconfigOut(cmd, &createConfig.kubeconfigOutFlags)

	return cmd
}

func createAction(appCtx *AppContext, config *CreateConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client := appCtx.Client
		name := args[0]

		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		if config.agents != 0 && config.mode != string(v1beta1.VirtualClusterMode) {
			return errors.New("invalid flag, --agents flag is only allowed in virtual mode")
		}

		if config.mode == string(v1beta1.HCPClusterMode) {
			logrus.Warn("HCP (Hosted Control Plane) mode is experimental.")
		}

		namespace := appCtx.Namespace(name)

		if err := createNamespace(ctx, client, namespace, config.policy); err != nil {
			return err
		}

		if strings.Contains(config.version, "+") {
			orig := config.version
			config.version = strings.ReplaceAll(config.version, "+", "-")
			logrus.Warnf("Invalid K3s docker reference version: '%s'. Using '%s' instead", orig, config.version)
		}

		if config.customCertsPath != "" {
			if err := CreateCustomCertsSecrets(ctx, name, namespace, config.customCertsPath, client); err != nil {
				return err
			}
		}

		logrus.Infof("Creating cluster '%s' in namespace '%s'", name, namespace)

		cluster, err := newCluster(name, namespace, config)
		if err != nil {
			return err
		}

		cluster.Spec.Expose = &v1beta1.ExposeConfig{
			NodePort: &v1beta1.NodePortConfig{},
		}

		// add Host IP address as an extra TLS-SAN to expose the k3k cluster
		host, err := extractHost(appCtx.RestConfig.Host, config.kubeconfigServerHost)
		if err != nil {
			return err
		}

		sans := append([]string{host}, config.tlsSANs...)

		cluster.Spec.TLSSANs = uniqueStrings(sans)

		if err := createCluster(ctx, client, cluster, config.token); err != nil {
			return err
		}

		if err := waitForClusterReconciled(ctx, client, cluster, config.timeout); err != nil {
			return fmt.Errorf("failed to wait for cluster to be reconciled: %w", err)
		}

		clusterDetails, err := getClusterDetails(cluster)
		if err != nil {
			return fmt.Errorf("failed to get cluster details: %w", err)
		}

		logrus.Info(clusterDetails)

		logrus.Infof("Waiting for cluster to be available..")

		if err := waitForClusterReady(ctx, client, cluster, config.timeout); err != nil {
			return fmt.Errorf("failed to wait for cluster to become ready (status: %s): %w", cluster.Status.Phase, err)
		}

		logrus.Infof("Extracting Kubeconfig for '%s' cluster", name)

		// retry every 5s for at most 2m, or 25 times
		availableBackoff := wait.Backoff{
			Duration: 5 * time.Second,
			Cap:      2 * time.Minute,
			Steps:    25,
		}

		var kubeconfig *clientcmdapi.Config

		err = retry.OnError(availableBackoff, apierrors.IsNotFound, func() error {
			kubeconfigSecretKey := types.NamespacedName{
				Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "kubeconfig"),
				Namespace: cluster.Namespace,
			}

			var kubeconfigSecret corev1.Secret
			if err := client.Get(ctx, kubeconfigSecretKey, &kubeconfigSecret); err != nil {
				return err
			}

			kubeconfig, err = clientcmd.Load(kubeconfigSecret.Data["kubeconfig.yaml"])

			return err
		})
		if err != nil {
			return err
		}

		serverURL, err := server.URL(ctx, client, cluster, host)
		if err != nil {
			return err
		}

		for _, kubeCluster := range kubeconfig.Clusters {
			kubeCluster.Server = serverURL.String()
		}

		if err := writeKubeconfig(appCtx, cluster, kubeconfig, config.standalonePath(cluster)); err != nil {
			return err
		}

		if cluster.Spec.Mode == v1beta1.HCPClusterMode {
			printHCPJoinInstructions(cluster, serverURL.String())
		}

		return nil
	}
}

func printHCPJoinInstructions(cluster *v1beta1.Cluster, serverURL string) {
	tokenSecretName := k3kcluster.TokenSecretName(cluster.Name)
	k3sVersion := controller.ResolveK3sVersion(cluster)

	logrus.Infof(`To join an external worker node to this HCP cluster:

	1. On this machine, fetch the cluster token:

		kubectl get secret -n %s %s -o jsonpath='{.data.token}' | base64 -d

	2. On the worker node, run (replace <TOKEN> with the value from step 1):

		curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=%s K3S_URL=%s K3S_TOKEN=<TOKEN> sh -

`, cluster.Namespace, tokenSecretName, k3sVersion.ReleaseName(), serverURL)
}

func newCluster(name, namespace string, config *CreateConfig) (*v1beta1.Cluster, error) {
	var storageRequestSize *resource.Quantity
	if config.storageRequestSize != "" {
		parsed, err := resource.ParseQuantity(config.storageRequestSize)
		if err != nil {
			return nil, err
		}

		storageRequestSize = new(parsed)
	}

	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      parseKeyValuePairs(config.labels, "label"),
			Annotations: parseKeyValuePairs(config.annotations, "annotation"),
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: "k3k.io/v1beta1",
		},
		Spec: v1beta1.ClusterSpec{
			Servers:     new(int32(config.servers)),
			Agents:      new(int32(config.agents)),
			ClusterCIDR: config.clusterCIDR,
			ServiceCIDR: config.serviceCIDR,
			ServerArgs:  config.serverArgs,
			AgentArgs:   config.agentArgs,
			ServerEnvs:  env(config.serverEnvs),
			AgentEnvs:   env(config.agentEnvs),
			Version:     config.version,
			Mode:        v1beta1.ClusterMode(config.mode),
			Persistence: v1beta1.PersistenceConfig{
				Type:               v1beta1.PersistenceMode(config.persistenceType),
				StorageClassName:   new(config.storageClassName),
				StorageRequestSize: storageRequestSize,
			},
			MirrorHostNodes: config.mirrorHostNodes,
		},
	}
	if config.storageClassName == "" {
		cluster.Spec.Persistence.StorageClassName = nil
	}

	if config.token != "" {
		cluster.Spec.TokenSecretRef = &corev1.SecretReference{
			Name:      k3kcluster.TokenSecretName(name),
			Namespace: namespace,
		}
	}

	if config.customCertsPath != "" {
		cluster.Spec.CustomCAs = &v1beta1.CustomCAs{
			Enabled: true,
			Sources: v1beta1.CredentialSources{
				ClientCA: v1beta1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "client-ca"),
				},
				ServerCA: v1beta1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "server-ca"),
				},
				EtcdServerCA: v1beta1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "etcd-server-ca"),
				},
				EtcdPeerCA: v1beta1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "etcd-peer-ca"),
				},
				RequestHeaderCA: v1beta1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "request-header-ca"),
				},
				ServiceAccountToken: v1beta1.CredentialSource{
					SecretName: controller.SafeConcatNameWithPrefix(cluster.Name, "service-account-token"),
				},
			},
		}
	}

	return cluster, nil
}

func env(envSlice []string) []corev1.EnvVar {
	var envVars []corev1.EnvVar

	for _, env := range envSlice {
		keyValue := strings.Split(env, "=")
		if len(keyValue) != 2 {
			logrus.Fatalf("incorrect value for environment variable %s", env)
		}

		envVars = append(envVars, corev1.EnvVar{
			Name:  keyValue[0],
			Value: keyValue[1],
		})
	}

	return envVars
}

func waitForClusterReconciled(ctx context.Context, k8sClient client.Client, cluster *v1beta1.Cluster, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		key := client.ObjectKeyFromObject(cluster)
		if err := k8sClient.Get(ctx, key, cluster); err != nil {
			return false, fmt.Errorf("failed to get resource: %w", err)
		}

		return cluster.Status.HostVersion != "", nil
	})
}

func waitForClusterReady(ctx context.Context, k8sClient client.Client, cluster *v1beta1.Cluster, timeout time.Duration) error {
	interval := 5 * time.Second
	failCount := 0

	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		key := client.ObjectKeyFromObject(cluster)
		if err := k8sClient.Get(ctx, key, cluster); err != nil {
			return false, fmt.Errorf("failed to get resource: %w", err)
		}

		// If resource ready -> stop polling
		if cluster.Status.Phase == v1beta1.ClusterReady {
			return true, nil
		}

		// If resource failed -> stop polling with an error
		if cluster.Status.Phase == v1beta1.ClusterFailed {
			failCount++

			if failCount > 3 {
				return true, fmt.Errorf("cluster creation failed: %s", cluster.Status.Phase)
			}

			return false, nil
		}

		// Condition not met, continue polling.
		return false, nil
	})
}

// createCluster creates the cluster and, with a custom token, the secret holding it. The
// cluster is made the owner of the token secret, so the secret is deleted with the cluster.
// A failed create can be run again: an existing token secret with the same token is reused.
func createCluster(ctx context.Context, k8sclient client.Client, cluster *v1beta1.Cluster, token string) error {
	var tokenSecretCreated bool

	if token != "" {
		logrus.Info("Creating cluster token secret")

		created, err := ensureTokenSecret(ctx, k8sclient, token, cluster.Name, cluster.Namespace)
		if err != nil {
			return err
		}

		tokenSecretCreated = created
	}

	if err := k8sclient.Create(ctx, cluster); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			if !tokenSecretCreated {
				return err
			}

			// without a cluster nothing would delete the token secret, but the cluster could
			// have been created even if the request failed, e.g. when the response was lost
			if isRejectedRequest(err) {
				deleteTokenSecret(ctx, k8sclient, cluster)
				return err
			}

			return fmt.Errorf("failed to create the cluster, run the command again to retry: %w", err)
		}

		logrus.Infof("Cluster '%s' already exists", cluster.Name)
	}

	if token == "" {
		return nil
	}

	if err := setTokenSecretOwner(ctx, k8sclient, cluster); err != nil {
		deleting := errors.Is(err, errClusterDeleting)

		// a deleted or deleting cluster will not own the token secret: nothing else would delete it
		if tokenSecretCreated && (deleting || apierrors.IsNotFound(err)) {
			deleteTokenSecret(ctx, k8sclient, cluster)

			if !deleting {
				return fmt.Errorf("the cluster was deleted while being created: %w", err)
			}
		}

		if deleting {
			return fmt.Errorf("%w, wait for it to be removed and run the command again", err)
		}

		return fmt.Errorf("failed to set the cluster as the owner of its token secret, run the command again to retry: %w", err)
	}

	return nil
}

// ensureTokenSecret creates the secret holding the custom token of a cluster, returning
// true if it was created. An existing secret is reused only if it holds the same token.
func ensureTokenSecret(ctx context.Context, k8sclient client.Client, token, name, namespace string) (bool, error) {
	tokenSecret := k3kcluster.TokenSecretObj(token, name, namespace)

	err := k8sclient.Create(ctx, &tokenSecret)
	if err == nil {
		return true, nil
	}

	if !apierrors.IsAlreadyExists(err) {
		return false, err
	}

	var existing corev1.Secret
	if err := k8sclient.Get(ctx, client.ObjectKeyFromObject(&tokenSecret), &existing); err != nil {
		return false, err
	}

	if string(existing.Data["token"]) != token {
		return false, fmt.Errorf("token secret %s/%s already exists with a different token", namespace, tokenSecret.Name)
	}

	return false, nil
}

// isRejectedRequest returns true if err is a client error returned by the API server, which
// rejected the request without persisting anything.
func isRejectedRequest(err error) bool {
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		return false
	}

	code := status.Status().Code

	return code >= http.StatusBadRequest && code < http.StatusInternalServerError && code != http.StatusRequestTimeout
}

// deleteTokenSecret deletes the token secret of the cluster, logging a failure.
func deleteTokenSecret(ctx context.Context, k8sclient client.Client, cluster *v1beta1.Cluster) {
	tokenSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: k3kcluster.TokenSecretName(cluster.Name), Namespace: cluster.Namespace},
	}

	if err := k8sclient.Delete(ctx, &tokenSecret); client.IgnoreNotFound(err) != nil {
		logrus.Warnf("Failed to delete the token secret %s/%s: %v", tokenSecret.Namespace, tokenSecret.Name, err)
	}
}

// errClusterDeleting is returned when the cluster to create already exists and is being deleted.
var errClusterDeleting = errors.New("the cluster is being deleted")

// setTokenSecretOwner makes the cluster the controller of its custom token secret, retrying
// on conflicts.
func setTokenSecretOwner(ctx context.Context, k8sclient client.Client, cluster *v1beta1.Cluster) error {
	key := types.NamespacedName{Name: k3kcluster.TokenSecretName(cluster.Name), Namespace: cluster.Namespace}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// fetch the cluster to get its UID, also when it already existed, on every attempt: the
		// cluster could have been deleted and created again with another UID in the meantime
		var owner v1beta1.Cluster
		if err := k8sclient.Get(ctx, client.ObjectKeyFromObject(cluster), &owner); err != nil {
			return err
		}

		// the token secret would be deleted with the old cluster
		if !owner.DeletionTimestamp.IsZero() {
			return fmt.Errorf("%w: %s/%s", errClusterDeleting, owner.Namespace, owner.Name)
		}

		var tokenSecret corev1.Secret
		if err := k8sclient.Get(ctx, key, &tokenSecret); err != nil {
			return err
		}

		if ref := metav1.GetControllerOf(&tokenSecret); ref != nil {
			if ref.UID == owner.UID {
				return nil
			}

			// a reference to a cluster with the same name but another UID is left by a deleted
			// cluster, as the name of a cluster is unique in its namespace
			if isClusterRef(*ref, owner.Name) {
				removeOwnerReference(&tokenSecret, ref.UID)
			}
		}

		// fails if the secret is controlled by another object
		if err := controllerutil.SetControllerReference(&owner, &tokenSecret, k8sclient.Scheme()); err != nil {
			return err
		}

		return k8sclient.Update(ctx, &tokenSecret)
	})
}

// isClusterRef returns true if ref refers to a k3k cluster with the given name.
func isClusterRef(ref metav1.OwnerReference, name string) bool {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return false
	}

	return gv.Group == v1beta1.SchemeGroupVersion.Group && ref.Kind == "Cluster" && ref.Name == name
}

// removeOwnerReference removes the owner reference with the given UID from obj.
func removeOwnerReference(obj metav1.Object, uid types.UID) {
	refs := slices.DeleteFunc(obj.GetOwnerReferences(), func(ref metav1.OwnerReference) bool {
		return ref.UID == uid
	})

	obj.SetOwnerReferences(refs)
}

// CreateCustomCertsSecrets creates the Secrets holding the custom CA certificates read
// from customCertsPath.
func CreateCustomCertsSecrets(ctx context.Context, name, namespace, customCertsPath string, k8sclient client.Client) error {
	customCAsMap := map[string]string{
		"etcd-peer-ca":          "/etcd/peer-ca",
		"etcd-server-ca":        "/etcd/server-ca",
		"server-ca":             "/server-ca",
		"client-ca":             "/client-ca",
		"request-header-ca":     "/request-header-ca",
		"service-account-token": "/service",
	}

	for certName, fileName := range customCAsMap {
		var (
			certFilePath, keyFilePath string
			cert, key                 []byte
			err                       error
		)

		if certName != "service-account-token" {
			certFilePath = customCertsPath + fileName + ".crt"

			cert, err = os.ReadFile(certFilePath)
			if err != nil {
				return err
			}
		}

		keyFilePath = customCertsPath + fileName + ".key"

		key, err = os.ReadFile(keyFilePath)
		if err != nil {
			return err
		}

		certSecret := caCertSecret(certName, name, namespace, cert, key)

		if err := k8sclient.Create(ctx, certSecret); err != nil {
			return client.IgnoreAlreadyExists(err)
		}
	}

	return nil
}

func caCertSecret(certName, clusterName, clusterNamespace string, cert, key []byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.SafeConcatNameWithPrefix(clusterName, certName),
			Namespace: clusterNamespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       cert,
			corev1.TLSPrivateKeyKey: key,
		},
	}
}

func parseKeyValuePairs(pairs []string, pairType string) map[string]string {
	resultMap := make(map[string]string)

	for _, p := range pairs {
		var k, v string

		keyValue := strings.SplitN(p, "=", 2)

		k = keyValue[0]
		if len(keyValue) == 2 {
			v = keyValue[1]
		}

		resultMap[k] = v

		logrus.Debugf("Adding '%s=%s' %s to Cluster", k, v, pairType)
	}

	return resultMap
}

const clusterDetailsTemplate = `Cluster details:
  Mode: {{ .Mode }}
  Servers: {{ .Servers }}{{ if .Agents }}
  Agents: {{ .Agents }}{{ end }}
  Version: {{ if .Version }}{{ .Version }}{{ else }}{{ .HostVersion }}{{ end }} (Host: {{ .HostVersion }})
  Persistence:
    Type: {{.Persistence.Type}}{{ if .Persistence.StorageClassName }}
    StorageClass: {{ .Persistence.StorageClassName }}{{ end }}{{ if .Persistence.StorageRequestSize }}
    Size: {{ .Persistence.StorageRequestSize }}{{ end }}{{ if .Labels }}
  Labels: {{ range $key, $value := .Labels }}
    {{$key}}: {{$value}}{{ end }}{{ end }}{{ if .Annotations }}
  Annotations: {{ range $key, $value := .Annotations }}
    {{$key}}: {{$value}}{{ end }}{{ end }}`

type persistenceConfig struct {
	Type               v1beta1.PersistenceMode
	StorageClassName   string
	StorageRequestSize string
}

func getClusterDetails(cluster *v1beta1.Cluster) (string, error) {
	type templateData struct {
		Mode        v1beta1.ClusterMode
		Servers     int32
		Agents      int32
		Version     string
		HostVersion string
		Persistence persistenceConfig
		Labels      map[string]string
		Annotations map[string]string
	}

	data := templateData{
		Mode:        cluster.Spec.Mode,
		Servers:     ptr.Deref(cluster.Spec.Servers, 0),
		Agents:      ptr.Deref(cluster.Spec.Agents, 0),
		Version:     cluster.Spec.Version,
		HostVersion: cluster.Status.HostVersion,
		Annotations: cluster.Annotations,
		Labels:      cluster.Labels,
	}

	data.Persistence.Type = cluster.Spec.Persistence.Type
	data.Persistence.StorageClassName = ptr.Deref(cluster.Spec.Persistence.StorageClassName, "")

	if srs := cluster.Spec.Persistence.StorageRequestSize; srs != nil {
		data.Persistence.StorageRequestSize = srs.String()
	}

	tmpl, err := template.New("clusterDetails").Parse(clusterDetailsTemplate)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// extractHost returns the hostname from the given server URL, stripping any
// port and correctly handling IPv6 addresses (e.g. "[2001:db8::1]:6443").
// If override is non-empty it is returned as-is.
func extractHost(serverURL, override string) (string, error) {
	if override != "" {
		return override, nil
	}

	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}

	return u.Hostname(), nil
}

// uniqueStrings returns a new slice with duplicates removed, preserving the first occurrence order.
func uniqueStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	result := make([]string, 0, len(ss))

	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}

	return result
}
