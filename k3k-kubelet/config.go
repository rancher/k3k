package main

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

// config has all virtual-kubelet startup options
type config struct {
	ClusterName       string `yaml:"clusterName,omitempty"`
	ClusterNamespace  string `yaml:"clusterNamespace,omitempty"`
	ServiceName       string `yaml:"serviceName,omitempty"`
	Token             string `yaml:"token,omitempty"`
	AgentHostname     string `yaml:"agentHostname,omitempty"`
	HostConfigPath    string `yaml:"hostConfigPath,omitempty"`
	VirtualConfigPath string `yaml:"virtualConfigPath,omitempty"`
	KubeletPort       int    `yaml:"kubeletPort,omitempty"`
	WebhookPort       int    `yaml:"webhookPort,omitempty"`
	ServerIP          string `yaml:"serverIP,omitempty"`
	Version           string `yaml:"version,omitempty"`
	MirrorHostNodes   bool   `yaml:"mirrorHostNodes,omitempty"`
}

func (c *config) unmarshalYAML(data []byte) error {
	var conf config

	if err := yaml.Unmarshal(data, &conf); err != nil {
		return err
	}

	if c.ClusterName == "" {
		c.ClusterName = conf.ClusterName
	}

	if c.ClusterNamespace == "" {
		c.ClusterNamespace = conf.ClusterNamespace
	}

	if c.HostConfigPath == "" {
		c.HostConfigPath = conf.HostConfigPath
	}

	if c.VirtualConfigPath == "" {
		c.VirtualConfigPath = conf.VirtualConfigPath
	}

	if c.KubeletPort == 0 {
		c.KubeletPort = conf.KubeletPort
	}

	if c.WebhookPort == 0 {
		c.WebhookPort = conf.WebhookPort
	}

	if c.AgentHostname == "" {
		c.AgentHostname = conf.AgentHostname
	}

	if c.ServiceName == "" {
		c.ServiceName = conf.ServiceName
	}

	if c.Token == "" {
		c.Token = conf.Token
	}

	if c.ServerIP == "" {
		c.ServerIP = conf.ServerIP
	}

	if c.Version == "" {
		c.Version = conf.Version
	}
	fmt.Printf("marshalled yaml: %v\n", conf.MirrorHostNodes)
	fmt.Printf("command line: %v\n", c.MirrorHostNodes)
	if !c.MirrorHostNodes {
		c.MirrorHostNodes = conf.MirrorHostNodes
	}

	return nil
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

func (c *config) parse(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return c.unmarshalYAML(b)
}
