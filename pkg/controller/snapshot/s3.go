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

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/k3s"
)

func (r *Reconciler) getS3ConfigFromSecret(ctx context.Context, snapshot *v1beta1.ETCDSnapshot) (*k3s.EtcdS3, error) {
	var s3Secret corev1.Secret

	// only work with secrets in the same namespace as snapshot
	secretKey := types.NamespacedName{
		Name:      snapshot.Spec.S3ConfigSecretRef.Name,
		Namespace: snapshot.Namespace,
	}

	if err := r.Get(ctx, secretKey, &s3Secret); err != nil {
		return nil, err
	}

	etcdS3 := k3s.EtcdS3{
		AccessKey:    string(s3Secret.Data["etcd-s3-access-key"]),
		Bucket:       string(s3Secret.Data["etcd-s3-bucket"]),
		BucketLookup: string(s3Secret.Data["etcd-s3-bucket-lookup-type"]),
		Endpoint:     k3s.DefaultEtcdS3.Endpoint,
		Folder:       string(s3Secret.Data["etcd-s3-folder"]),
		Proxy:        string(s3Secret.Data["etcd-s3-proxy"]),
		Region:       k3s.DefaultEtcdS3.Region,
		SecretKey:    string(s3Secret.Data["etcd-s3-secret-key"]),
		SessionToken: string(s3Secret.Data["etcd-s3-session-token"]),
		Retention:    k3s.DefaultEtcdS3.Retention,
		Timeout:      *k3s.DefaultEtcdS3.Timeout.DeepCopy(),
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
		if duration, err := time.ParseDuration(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-timeout value from S3 config secret: %w", err)
		} else {
			etcdS3.Timeout.Duration = duration
		}
	}

	if v, ok := s3Secret.Data["etcd-s3-retention"]; ok {
		if retention, err := strconv.Atoi(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-retention value from S3 config secret: %w", err)
		} else {
			etcdS3.Retention = retention
		}
	}

	// configure ssl verification, if value can be parsed
	if v, ok := s3Secret.Data["etcd-s3-skip-ssl-verify"]; ok {
		if b, err := strconv.ParseBool(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-skip-ssl-verify value from S3 config secret: %w", err)
		} else {
			etcdS3.SkipSSLVerify = b
		}
	}

	// configure insecure http, if value can be parsed
	if v, ok := s3Secret.Data["etcd-s3-insecure"]; ok {
		if b, err := strconv.ParseBool(string(v)); err != nil {
			return nil, fmt.Errorf("failed to parse etcd-s3-insecure value from S3 config secret: %w", err)
		} else {
			etcdS3.Insecure = b
		}
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
		if err := r.Get(ctx, types.NamespacedName{Name: caConfigMapName, Namespace: s3Secret.Namespace}, &configMap); err != nil {
			return nil, fmt.Errorf("failed to get ConfigMap %s for etcd-s3-endpoint-ca-name value from S3 config secret %s: %w", caConfigMapName, s3Secret.Name, err)
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
