package snapshot

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/k3s"
)

func (r *SnapshotReconciler) getS3ConfigFromSecret(ctx context.Context, s3ConfigSecret *corev1.Secret) (*k3s.EtcdS3, error) {
	etcdS3 := k3s.EtcdS3{
		AccessKey:    string(s3ConfigSecret.Data["etcd-s3-access-key"]),
		Bucket:       string(s3ConfigSecret.Data["etcd-s3-bucket"]),
		BucketLookup: string(s3ConfigSecret.Data["etcd-s3-bucket-lookup-type"]),
		Endpoint:     k3s.DefaultEtcdS3.Endpoint,
		Folder:       string(s3ConfigSecret.Data["etcd-s3-folder"]),
		Proxy:        string(s3ConfigSecret.Data["etcd-s3-proxy"]),
		Region:       k3s.DefaultEtcdS3.Region,
		SecretKey:    string(s3ConfigSecret.Data["etcd-s3-secret-key"]),
		SessionToken: string(s3ConfigSecret.Data["etcd-s3-session-token"]),
		Retention:    k3s.DefaultEtcdS3.Retention,
		Timeout:      *k3s.DefaultEtcdS3.Timeout.DeepCopy(),
	}

	// Set endpoint from secret if set
	if v, ok := s3ConfigSecret.Data["etcd-s3-endpoint"]; ok {
		etcdS3.Endpoint = string(v)
	}

	// Set region from secret if set
	if v, ok := s3ConfigSecret.Data["etcd-s3-region"]; ok {
		etcdS3.Region = string(v)
	}

	// Set timeout from secret if set
	if v, ok := s3ConfigSecret.Data["etcd-s3-timeout"]; ok {
		if duration, err := time.ParseDuration(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-timeout value from S3 config secret: %w", err)
		} else {
			etcdS3.Timeout.Duration = duration
		}
	}

	if v, ok := s3ConfigSecret.Data["etcd-s3-retention"]; ok {
		if retention, err := strconv.Atoi(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-retention value from S3 config secret: %w", err)
		} else {
			etcdS3.Retention = retention
		}
	}

	// configure ssl verification, if value can be parsed
	if v, ok := s3ConfigSecret.Data["etcd-s3-skip-ssl-verify"]; ok {
		if b, err := strconv.ParseBool(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-skip-ssl-verify value from S3 config secret: %w", err)
		} else {
			etcdS3.SkipSSLVerify = b
		}
	}

	// configure insecure http, if value can be parsed
	if v, ok := s3ConfigSecret.Data["etcd-s3-insecure"]; ok {
		if b, err := strconv.ParseBool(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-insecure value from S3 config secret %w", err)
		} else {
			etcdS3.Insecure = b
		}
	}

	// encode CA bundles from value, and keys in configmap if one is named
	caBundles := []string{}
	// Add inline CA bundle if set
	if len(s3ConfigSecret.Data["etcd-s3-endpoint-ca"]) > 0 {
		caBundles = append(caBundles, base64.StdEncoding.EncodeToString(s3ConfigSecret.Data["etcd-s3-endpoint-ca"]))
	}

	// Add CA bundles from named configmap if set
	if caConfigMapName := string(s3ConfigSecret.Data["etcd-s3-endpoint-ca-name"]); caConfigMapName != "" {
		var configMap corev1.ConfigMap
		if err := r.Client.Get(ctx, types.NamespacedName{Name: caConfigMapName, Namespace: s3ConfigSecret.Namespace}, &configMap); err != nil {
			return nil, fmt.Errorf("failed to get ConfigMap %s for etcd-s3-endpoint-ca-name value from S3 config secret %s: %w", caConfigMapName, s3ConfigSecret.Name, err)
		} else {
			for _, v := range configMap.Data {
				caBundles = append(caBundles, base64.StdEncoding.EncodeToString([]byte(v)))
			}

			for _, v := range configMap.BinaryData {
				caBundles = append(caBundles, base64.StdEncoding.EncodeToString(v))
			}
		}
	}

	// Concatenate all requested CA bundle strings into config var
	etcdS3.EndpointCA = strings.Join(caBundles, " ")

	return &etcdS3, nil
}
