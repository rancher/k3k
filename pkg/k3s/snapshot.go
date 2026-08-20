package k3s

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	k3sv1 "github.com/k3s-io/api/k3s.cattle.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// redefining the EtcdS3 Configuration to avoid k3s-io/k3s dependency
type EtcdS3 struct {
	AccessKey     string          `json:"accessKey,omitempty"`
	Bucket        string          `json:"bucket,omitempty"`
	BucketLookup  string          `json:"bucketLookup,omitempty"`
	Endpoint      string          `json:"endpoint,omitempty"`
	EndpointCA    string          `json:"endpointCA,omitempty"`
	Folder        string          `json:"folder,omitempty"`
	Proxy         string          `json:"proxy,omitempty"`
	Region        string          `json:"region,omitempty"`
	SecretKey     string          `json:"secretKey,omitempty"`
	SessionToken  string          `json:"sessionToken,omitempty"`
	Insecure      bool            `json:"insecure,omitempty"`
	SkipSSLVerify bool            `json:"skipSSLVerify,omitempty"`
	Retention     int             `json:"retention,omitempty"`
	Timeout       metav1.Duration `json:"timeout"`
}

// DefaultEtcdS3 is the default S3 configuration used for snapshot
// operations when no configuration is provided.
var DefaultEtcdS3 = &EtcdS3{
	Endpoint: "s3.amazonaws.com",
	Region:   "us-east-1",
	Timeout: metav1.Duration{
		Duration: 5 * time.Minute,
	},
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

func (c *Client) SaveSnapshot(snapshot *v1beta1.EtcdSnapshot, s3Config *EtcdS3) (*SnapshotResult, error) {
	req := snapshotRequest{
		Operation: snapshotOperationSave,
		Name:      []string{snapshot.Name},
		Compress:  new(snapshot.Spec.Compress),
		S3:        s3Config,
	}

	snapshotResult, err := do[*SnapshotResult](c, etcdSnapshotEndpoint, "server", http.MethodPost, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSaveSnapshot, err)
	}

	return snapshotResult, nil
}

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

func (c *Client) DeleteSnapshot(snapshot *v1beta1.EtcdSnapshot, s3Config *EtcdS3) (*SnapshotResult, error) {
	req := snapshotRequest{
		Operation: snapshotOperationDelete,
		Name:      []string{snapshot.Status.Filename},
		S3:        s3Config,
	}

	snapshotResult, err := do[*SnapshotResult](c, etcdSnapshotEndpoint, "server", http.MethodPost, req)
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

type SnapshotResult struct {
	Created []string `json:"created,omitempty"`
	Deleted []string `json:"deleted,omitempty"`
}
