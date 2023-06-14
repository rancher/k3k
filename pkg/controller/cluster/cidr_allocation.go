package cluster

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	"k8s.io/apimachinery/pkg/types"
)

const (
	cidrAllocationClusterPoolName = "k3k-cluster-cidr-allocation-pool"
	cidrAllocationServicePoolName = "k3k-service-cidr-allocation-pool"

	defaultClusterCIDR        = "10.44.0.0/16"
	defaultClusterServiceCIDR = "10.45.0.0/16"
)

// determineOctet dertermines the octet for the
// given mask bits of a subnet.
func determineOctet(mb int) uint8 {
	switch {
	case mb <= 8:
		return 1
	case mb >= 8 && mb <= 16:
		return 2
	case mb >= 8 && mb <= 24:
		return 3
	case mb >= 8 && mb <= 32:
		return 4
	default:
		return 0
	}
}

// generateSubnets generates all subnets for the given CIDR.
func generateSubnets(cidr string) ([]string, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	usedBits, _ := ipNet.Mask.Size()

	octet := determineOctet(usedBits)

	ip := ipNet.IP.To4()
	octetVal := ip[octet-1]

	var subnets []string

	for i := octetVal; i < 254; i++ {
		octetVal++
		ip[octet-1] = octetVal
		subnets = append(subnets, fmt.Sprintf("%s/%d", ip, usedBits))
	}

	return subnets, nil
}

// nextCIDR retrieves the next available CIDR address from the given pool.
func (c *ClusterReconciler) nextCIDR(ctx context.Context, cidrAllocationPoolName, clusterName string) (*net.IPNet, error) {
	var cidrPool v1alpha1.CIDRAllocationPool

	nn := types.NamespacedName{
		Name: cidrAllocationPoolName,
	}
	if err := c.Client.Get(ctx, nn, &cidrPool); err != nil {
		return nil, util.WrapErr("failed to get cidrpool", err)
	}

	var ipNet *net.IPNet

	for _, pool := range cidrPool.Status.Pool {
		if pool.ClusterName == clusterName {
			_, ipn, err := net.ParseCIDR(pool.IPNet)
			if err != nil {
				return nil, util.WrapErr("failed to parse cidr", err)
			}
			return ipn, nil
		}
	}
	for i := 0; i < len(cidrPool.Status.Pool); i++ {
		if cidrPool.Status.Pool[i].ClusterName == "" && cidrPool.Status.Pool[i].Issued == 0 {
			cidrPool.Status.Pool[i].ClusterName = clusterName
			cidrPool.Status.Pool[i].Issued = time.Now().Unix()

			_, ipn, err := net.ParseCIDR(cidrPool.Status.Pool[i].IPNet)
			if err != nil {
				return nil, util.WrapErr("failed to parse cidr", err)
			}
			if err := c.Client.Update(ctx, &cidrPool); err != nil {
				return nil, util.WrapErr("failed to update cidr pool", err)
			}
			ipNet = ipn

			break
		}
	}

	return ipNet, nil
}

// releaseCIDR updates the given CIDR pool by marking the address as available.
func (c *ClusterReconciler) releaseCIDR(ctx context.Context, cidrAllocationPoolName, clusterName string) error {
	var cidrPool v1alpha1.CIDRAllocationPool

	nn := types.NamespacedName{
		Name: cidrAllocationPoolName,
	}
	if err := c.Client.Get(ctx, nn, &cidrPool); err != nil {
		return err
	}

	for i := 0; i < len(cidrPool.Status.Pool); i++ {
		if cidrPool.Status.Pool[i].ClusterName == clusterName {
			cidrPool.Status.Pool[i].ClusterName = ""
			cidrPool.Status.Pool[i].Issued = 0
		}

		if err := c.Client.Status().Update(ctx, &cidrPool); err != nil {
			return err
		}
	}

	return nil
}
