package k3s

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k3sv1 "github.com/k3s-io/api/k3s.cattle.io/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// EtcdS3 is the S3 configuration for etcd snapshots. It redefines the k3s type of the
// same name to avoid taking a dependency on k3s-io/k3s.
type EtcdS3 struct {
	AccessKey     string `json:"accessKey,omitempty" yaml:"etcd-s3-access-key,omitempty"`
	Bucket        string `json:"bucket,omitempty" yaml:"etcd-s3-bucket,omitempty"`
	BucketLookup  string `json:"bucketLookup,omitempty" yaml:"etcd-s3-bucket-lookup-type,omitempty"`
	Endpoint      string `json:"endpoint,omitempty" yaml:"etcd-s3-endpoint,omitempty"`
	EndpointCA    string `json:"endpointCA,omitempty" yaml:"etcd-s3-endpoint-ca,omitempty"`
	Folder        string `json:"folder,omitempty" yaml:"etcd-s3-folder,omitempty"`
	Proxy         string `json:"proxy,omitempty" yaml:"etcd-s3-proxy,omitempty"`
	Region        string `json:"region,omitempty" yaml:"etcd-s3-region,omitempty"`
	SecretKey     string `json:"secretKey,omitempty" yaml:"etcd-s3-secret-key,omitempty"`
	SessionToken  string `json:"sessionToken,omitempty" yaml:"etcd-s3-session-token,omitempty"`
	Insecure      bool   `json:"insecure,omitempty" yaml:"etcd-s3-insecure,omitempty"`
	SkipSSLVerify bool   `json:"skipSSLVerify,omitempty" yaml:"etcd-s3-skip-ssl-verify,omitempty"`
	Retention     int    `json:"retention,omitempty" yaml:"etcd-s3-retention,omitempty"`
	Timeout       string `json:"timeout" yaml:"etcd-s3-timeout,omitempty"`
}

// DefaultEtcdS3 is the default S3 configuration used for snapshot
// operations when no configuration is provided.
var DefaultEtcdS3 = &EtcdS3{
	Endpoint:  "s3.amazonaws.com",
	Region:    "us-east-1",
	Timeout:   (5 * time.Minute).String(),
	Retention: 5,
}

var (
	// ErrSaveSnapshot is an error of a failed save snapshot request
	ErrSaveSnapshot = errors.New("failed to execute save snapshot request")
	// ErrListSnapshots is an error of a failed list snapshots request
	ErrListSnapshots = errors.New("failed to execute list snapshots request")
	// ErrDeleteSnapshot is an error of a failed delete snapshot request
	ErrDeleteSnapshot = errors.New("failed to execute delete snapshot request")
	// ErrSnapshotNotFound is an error of snapshot not found in the k3s cluster
	ErrSnapshotNotFound = errors.New("snapshot not found")
)

type snapshotOperation string

const (
	etcdSnapshotEndpoint = "/db/snapshot"

	snapshotOperationSave   snapshotOperation = "save"
	snapshotOperationList   snapshotOperation = "list"
	snapshotOperationDelete snapshotOperation = "delete"
)

// SaveSnapshot asks the k3s server to save an etcd snapshot, storing it on S3 when
// s3Config is set. It wraps ErrSaveSnapshot on failure.
func (c *Client) SaveSnapshot(snapshot *v1beta1.EtcdSnapshot, s3Config *EtcdS3) (*SnapshotResponse, error) {
	req := snapshotRequest{
		Operation: snapshotOperationSave,
		Name:      []string{snapshot.Name},
		Compress:  new(snapshot.Spec.Compress),
		S3:        s3Config,
	}

	snapshotResult, err := do[*SnapshotResponse](c, etcdSnapshotEndpoint, "server", http.MethodPost, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSaveSnapshot, err)
	}

	return snapshotResult, nil
}

// ListSnapshots returns the etcd snapshots known to the k3s server, including those on
// S3 when s3Config is set. It wraps ErrListSnapshots on failure.
func (c *Client) ListSnapshots(s3Config *EtcdS3) (*k3sv1.ETCDSnapshotFileList, error) {
	req := snapshotRequest{
		Operation: snapshotOperationList,
		S3:        s3Config,
	}

	snapshotFileList, err := do[*k3sv1.ETCDSnapshotFileList](c, etcdSnapshotEndpoint, "server", http.MethodPost, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrListSnapshots, err)
	}

	return snapshotFileList, nil
}

// DeleteSnapshot deletes the snapshot file recorded in the snapshot status. It returns
// ErrSnapshotNotFound if the server does not report the file as deleted.
func (c *Client) DeleteSnapshot(snapshot *v1beta1.EtcdSnapshot, s3Config *EtcdS3) (*SnapshotResponse, error) {
	req := snapshotRequest{
		Operation: snapshotOperationDelete,
		Name:      []string{snapshot.Status.Filename},
		S3:        s3Config,
	}

	snapshotResult, err := do[*SnapshotResponse](c, etcdSnapshotEndpoint, "server", http.MethodPost, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDeleteSnapshot, err)
	}

	if !slices.Contains(snapshotResult.Deleted, snapshot.Status.Filename) {
		return nil, ErrSnapshotNotFound
	}

	return snapshotResult, nil
}

type snapshotRequest struct {
	Operation snapshotOperation `json:"operation"`
	Name      []string          `json:"name,omitempty"`
	Compress  *bool             `json:"compress,omitempty"`
	S3        *EtcdS3           `json:"s3,omitempty"`
}

// SnapshotResponse is the k3s server's reply to a snapshot save or delete request.
type SnapshotResponse struct {
	Created []string `json:"created,omitempty"`
	Deleted []string `json:"deleted,omitempty"`
}

// GetS3ConfigFromSecret reads the etcd s3 configuration from the snapshot secret and creates ETCD configuration
func GetS3ConfigFromSecret(ctx context.Context, client client.Client, snapshot *v1beta1.EtcdSnapshot) (*EtcdS3, error) {
	var s3Secret corev1.Secret

	// only work with secrets in the same namespace as snapshot
	secretKey := types.NamespacedName{
		Name:      snapshot.Spec.S3ConfigSecretRef.Name,
		Namespace: snapshot.Namespace,
	}

	if err := client.Get(ctx, secretKey, &s3Secret); err != nil {
		return nil, err
	}

	etcdS3 := EtcdS3{
		AccessKey:    string(s3Secret.Data["etcd-s3-access-key"]),
		Bucket:       string(s3Secret.Data["etcd-s3-bucket"]),
		BucketLookup: string(s3Secret.Data["etcd-s3-bucket-lookup-type"]),
		Endpoint:     DefaultEtcdS3.Endpoint,
		Folder:       string(s3Secret.Data["etcd-s3-folder"]),
		Proxy:        string(s3Secret.Data["etcd-s3-proxy"]),
		Region:       DefaultEtcdS3.Region,
		Retention:    DefaultEtcdS3.Retention,
		SecretKey:    string(s3Secret.Data["etcd-s3-secret-key"]),
		SessionToken: string(s3Secret.Data["etcd-s3-session-token"]),
		Timeout:      DefaultEtcdS3.Timeout,
	}

	// Set endpoint from secret if set
	if v, ok := s3Secret.Data["etcd-s3-endpoint"]; ok {
		etcdS3.Endpoint = string(v)
	}

	// Set region from secret if set
	if v, ok := s3Secret.Data["etcd-s3-region"]; ok {
		etcdS3.Region = string(v)
	}

	// Set timeout from secret if set
	if v, ok := s3Secret.Data["etcd-s3-timeout"]; ok {
		duration, err := time.ParseDuration(string(v))
		if err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-timeout value from S3 config secret: %w", err)
		}

		etcdS3.Timeout = duration.String()
	}

	if v, ok := s3Secret.Data["etcd-s3-retention"]; ok {
		retention, err := strconv.Atoi(string(v))
		if err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-retention value from S3 config secret: %w", err)
		}

		etcdS3.Retention = retention
	}

	// configure ssl verification, if value can be parsed
	if v, ok := s3Secret.Data["etcd-s3-skip-ssl-verify"]; ok {
		b, err := strconv.ParseBool(string(v))
		if err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-skip-ssl-verify value from S3 config secret: %w", err)
		}

		etcdS3.SkipSSLVerify = b
	}

	// configure insecure http, if value can be parsed
	if v, ok := s3Secret.Data["etcd-s3-insecure"]; ok {
		b, err := strconv.ParseBool(string(v))
		if err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-insecure value from S3 config secret: %w", err)
		}

		etcdS3.Insecure = b
	}

	// encode CA bundles from value, and keys in configmap if one is named
	caBundles := []string{}
	// Add inline CA bundle if set
	if len(s3Secret.Data["etcd-s3-endpoint-ca"]) > 0 {
		caBundles = append(caBundles, base64.StdEncoding.EncodeToString(s3Secret.Data["etcd-s3-endpoint-ca"]))
	}

	// Add CA bundles from named configmap if set
	if caConfigMapName := string(s3Secret.Data["etcd-s3-endpoint-ca-name"]); caConfigMapName != "" {
		var configMap corev1.ConfigMap
		if err := client.Get(ctx, types.NamespacedName{Name: caConfigMapName, Namespace: s3Secret.Namespace}, &configMap); err != nil {
			return nil, fmt.Errorf("failed to get ConfigMap %s for etcd-s3-endpoint-ca-name value from S3 config secret %s: %w", caConfigMapName, s3Secret.Name, err)
		}

		for _, v := range configMap.Data {
			caBundles = append(caBundles, base64.StdEncoding.EncodeToString([]byte(v)))
		}

		for _, v := range configMap.BinaryData {
			caBundles = append(caBundles, base64.StdEncoding.EncodeToString(v))
		}
	}

	// Concatenate all requested CA bundle strings into config var
	etcdS3.EndpointCA = strings.Join(caBundles, " ")

	return &etcdS3, nil
}
