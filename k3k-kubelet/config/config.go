package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v2"
)

// Config has all virtual-kubelet startup options
type Config struct {
	ClusterName       string `yaml:"clusterName"`
	ClusterNamespace  string `yaml:"clusterNamespace"`
	HostConfigPath    string `yaml:"hostConfigPath"`
	VirtualConfigPath string `yaml:"virtualConfigPath"`
	KubeletPort       string `yaml:"kubeletPort"`
	NodeName          string `yaml:"nodeName"`
	AgentPodIP        string `yaml:"agentPodIP"`
	Token             string `yaml:"token"`
}

func (t *Config) UnmarshalYAML(data []byte) error {
	var c Config
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
	if t.NodeName == "" {
		t.NodeName = c.NodeName
	}

	return nil
}

func (t *Config) Validate() error {
	if t.ClusterName == "" {
		return errors.New("cluster name is not provided")
	}
	if t.ClusterNamespace == "" {
		return errors.New("cluster namespace is not provided")
	}
	if t.AgentPodIP == "" {
		return errors.New("agent POD IP is not provided")
	}
	return nil
}

func (t *Config) Parse(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	configFileBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return t.UnmarshalYAML(configFileBytes)
}
