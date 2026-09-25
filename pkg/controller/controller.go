// Package controller holds the helpers shared by the k3k controllers, such as k3s
// image and version resolution and length-safe name generation.
package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	namePrefix = "k3k"
	// AdminCommonName is the common name of the admin certificate of a virtual cluster.
	AdminCommonName = "system:admin"
)

// Backoff is the cluster creation duration backoff
var Backoff = wait.Backoff{
	Steps:    5,
	Duration: 5 * time.Second,
	Factor:   2,
	Jitter:   0.1,
}

// K3sVersion encapsulates the resolved version metadata for a child K3s cluster.
type K3sVersion struct {
	// Raw holds the resolved version string (e.g. "v1.31.1-k3s1" or "latest").
	Raw string
}

// ResolveK3sVersion extracts and resolves the target K3s version from the Cluster CR.
// It prioritizes cluster.Spec.Version, falls back to cluster.Status.HostVersion with
// a default "-k3s1" release suffix, and defaults to "latest" if neither is populated.
func ResolveK3sVersion(cluster *v1beta1.Cluster) K3sVersion {
	if cluster.Spec.Version != "" {
		return K3sVersion{Raw: cluster.Spec.Version}
	}

	if cluster.Status.HostVersion != "" {
		return K3sVersion{Raw: cluster.Status.HostVersion + "-k3s1"}
	}

	return K3sVersion{Raw: "latest"}
}

// ImageTag returns the version string formatted for container image references
// (e.g., v1.31.1-k3s1).
func (v K3sVersion) ImageTag() string {
	return v.Raw
}

// ReleaseName returns the version string formatted for GitHub releases using the '+' delimiter
// (e.g., "v1.31.1+k3s1").
func (v K3sVersion) ReleaseName() string {
	return strings.Replace(v.Raw, "-", "+", 1)
}

// KubernetesVersion extracts the base semver string by stripping both build ('+') and prerelease/k3s ('-') metadata tags
// (e.g., "v1.31.1").
func (v K3sVersion) KubernetesVersion() string {
	return strings.Split(strings.Split(v.Raw, "-")[0], "+")[0]
}

// K3SImage returns the rancher/k3s image tagged with the found K3SVersion.
func K3SImage(cluster *v1beta1.Cluster, k3SImage string) string {
	k3sVersion := ResolveK3sVersion(cluster)
	return k3SImage + ":" + k3sVersion.ImageTag()
}

// FilterDNSNames returns only the DNS names of the given list, dropping the IP addresses.
// It is useful for the fields that cannot hold an IP, like the hosts of an Ingress.
func FilterDNSNames(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool {
		return net.ParseIP(name) != nil
	})
}

// SafeConcatNameWithPrefix runs the SafeConcatName with extra prefix.
func SafeConcatNameWithPrefix(name ...string) string {
	return SafeConcatName(append([]string{namePrefix}, name...)...)
}

// SafeConcatName concatenates the given strings and ensures the returned name is under 64 characters
// by cutting the string off at 57 characters and setting the last 6 with an encoded version of the concatenated string.
// Empty strings in the array will be ignored.
func SafeConcatName(name ...string) string {
	name = slices.DeleteFunc(name, func(s string) bool {
		return s == ""
	})

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
