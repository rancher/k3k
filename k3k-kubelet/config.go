package main

import (
	"errors"
	"os"

	"gopkg.in/yaml.v2"
)

// Config has all virtual-kubelet startup options
type config struct {
	ClusterName       string `yaml:"clusterName,omitempty"`
	ClusterNamespace  string `yaml:"clusterNamespace,omitempty"`
	NodeName          string `yaml:"nodeName,omitempty"`
	Token             string `yaml:"token,omitempty"`
	AgentHostname     string `yaml:"agentHostname,omitempty"`
	HostConfigPath    string `yaml:"hostConfigPath,omitempty"`
	VirtualConfigPath string `yaml:"virtualConfigPath,omitempty"`
	KubeletPort       string `yaml:"kubeletPort,omitempty"`
}

func (t *config) unmarshalYAML(data []byte) error {
	var c config

	if err := yaml.Unmarshal(data, &c); err != nil {
		return err
	}

	if t.ClusterName == "" {
		t.ClusterName = c.ClusterName
	}
	if t.ClusterNamespace == "" {
		t.ClusterNamespace = c.ClusterNamespace
	}
	if t.HostConfigPath == "" {
		t.HostConfigPath = c.HostConfigPath
	}
	if t.VirtualConfigPath == "" {
		t.VirtualConfigPath = c.VirtualConfigPath
	}
	if t.KubeletPort == "" {
		t.KubeletPort = c.KubeletPort
	}
	if t.AgentHostname == "" {
		t.AgentHostname = c.AgentHostname
	}
	if t.NodeName == "" {
		t.NodeName = c.NodeName
	}
	return nil
}

func (t *config) Validate() error {
	if t.ClusterName == "" {
		return errors.New("cluster name is not provided")
	}
	if t.ClusterNamespace == "" {
		return errors.New("cluster namespace is not provided")
	}
	if t.AgentHostname == "" {
		return errors.New("agent Hostname is not provided")
	}
	return nil
}

func (t *config) Parse(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	configFileBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return t.unmarshalYAML(configFileBytes)
}
