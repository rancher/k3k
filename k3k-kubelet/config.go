package main

import (
	"errors"
)

// config has all virtual-kubelet startup options
type config struct {
	ClusterName      string `mapstructure:"clusterName"`
	ClusterNamespace string `mapstructure:"clusterNamespace"`
	ServiceName      string `mapstructure:"serviceName"`
	Token            string `mapstructure:"token"`
	AgentHostname    string `mapstructure:"agentHostname"`
	HostKubeconfig   string `mapstructure:"hostKubeconfig"`
	VirtKubeconfig   string `mapstructure:"virtKubeconfig"`
	KubeletPort      int    `mapstructure:"kubeletPort"`
	WebhookPort      int    `mapstructure:"webhookPort"`
	ServerIP         string `mapstructure:"serverIP"`
	Version          string `mapstructure:"version"`
	MirrorHostNodes  bool   `mapstructure:"mirrorHostNodes"`
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
