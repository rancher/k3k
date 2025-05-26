package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	namePrefix      = "k3k"
	AdminCommonName = "system:admin"
)

// Backoff is the cluster creation duration backoff
var Backoff = wait.Backoff{
	Steps:    5,
	Duration: 5 * time.Second,
	Factor:   2,
	Jitter:   0.1,
}

// K3SImage returns the rancher/k3s image tagged with the specified Version.
// If Version is empty it will use with the same k8s version of the host cluster,
// stored in the Status object. It will return the untagged version as last fallback.
func K3SImage(cluster *v1alpha1.Cluster, k3SImage string) string {
	if cluster.Spec.Version != "" {
		return k3SImage + ":" + cluster.Spec.Version
	}

	if cluster.Status.HostVersion != "" {
		return k3SImage + ":" + cluster.Status.HostVersion
	}

	return k3SImage
}

// SafeConcatNameWithPrefix runs the SafeConcatName with extra prefix.
func SafeConcatNameWithPrefix(name ...string) string {
	return SafeConcatName(append([]string{namePrefix}, name...)...)
}

// SafeConcatName concatenates the given strings and ensures the returned name is under 64 characters
// by cutting the string off at 57 characters and setting the last 6 with an encoded version of the concatenated string.
func SafeConcatName(name ...string) string {
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
