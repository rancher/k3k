package main

import (
	"errors"
)

// config has all virtual-kubelet startup options
type config struct {
	ClusterName      string `mapstructure:"clustername"`
	ClusterNamespace string `mapstructure:"clusternamespace"`
	ServiceName      string `mapstructure:"servicename"`
	Token            string `mapstructure:"token"`
	AgentHostname    string `mapstructure:"agenthostname"`
	HostKubeconfig   string `mapstructure:"hostkubeconfig"`
	VirtKubeconfig   string `mapstructure:"virtkubeconfig"`
	KubeletPort      int    `mapstructure:"kubeletport"`
	WebhookPort      int    `mapstructure:"webhookport"`
	ServerIP         string `mapstructure:"serverip"`
	Version          string `mapstructure:"version"`
	MirrorHostNodes  bool   `mapstructure:"mirrorhostnodes"`
}

func (c *config) validate() error {
	if c.ClusterName == "" {
		return errors.New("cluster name is not provided")
	}

	if c.ClusterNamespace == "" {
		return errors.New("cluster namespace is not provided")
	}

	if c.AgentHostname == "" {
		return errors.New("agent Hostname is not provided")
	}

	return nil
}
