package main

import (
	"errors"
	"os"

	"gopkg.in/yaml.v2"
)

// config has all virtual-kubelet startup options
type config struct {
	ClusterName       string `yaml:"clusterName,omitempty"`
	ClusterNamespace  string `yaml:"clusterNamespace,omitempty"`
	NodeName          string `yaml:"nodeName,omitempty"`
	Token             string `yaml:"token,omitempty"`
	AgentHostname     string `yaml:"agentHostname,omitempty"`
	HostConfigPath    string `yaml:"hostConfigPath,omitempty"`
	VirtualConfigPath string `yaml:"virtualConfigPath,omitempty"`
	KubeletPort       string `yaml:"kubeletPort,omitempty"`
	AgentIP           string `yaml:"agentIP,omitempty"`
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
	if c.KubeletPort == "" {
		c.KubeletPort = conf.KubeletPort
	}
	if c.AgentHostname == "" {
		c.AgentHostname = conf.AgentHostname
	}
	if c.NodeName == "" {
		c.NodeName = conf.NodeName
	}
	if c.Token == "" {
		c.Token = conf.Token
	}
	if c.AgentIP == "" {
		c.AgentIP = conf.AgentIP
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
