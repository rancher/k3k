package main

import (
	"errors"
	"os"

	"gopkg.in/yaml.v2"
)

// Config has all virtual-kubelet startup options
type config struct {
	clusterName       string `yaml:"clusterName"`
	clusterNamespace  string `yaml:"clusterNamespace"`
	hostConfigPath    string `yaml:"hostConfigPath"`
	virtualConfigPath string `yaml:"virtualConfigPath"`
	kubeletPort       string `yaml:"kubeletPort"`
	nodeName          string `yaml:"nodeName"`
	agentPodIP        string `yaml:"agentPodIP"`
	token             string `yaml:"token"`
}

func (t *config) UnmarshalYAML(data []byte) error {
	var c config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	if t.clusterName == "" {
		t.clusterName = c.clusterName
	}
	if t.clusterNamespace == "" {
		t.clusterNamespace = c.clusterNamespace
	}
	if t.hostConfigPath == "" {
		t.hostConfigPath = c.hostConfigPath
	}
	if t.virtualConfigPath == "" {
		t.virtualConfigPath = c.virtualConfigPath
	}
	if t.kubeletPort == "" {
		t.kubeletPort = c.kubeletPort
	}
	if t.nodeName == "" {
		t.nodeName = c.nodeName
	}

	return nil
}

func (t *config) Validate() error {
	if t.clusterName == "" {
		return errors.New("cluster name is not provided")
	}
	if t.clusterNamespace == "" {
		return errors.New("cluster namespace is not provided")
	}
	if t.agentPodIP == "" {
		return errors.New("agent POD IP is not provided")
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
	return t.UnmarshalYAML(configFileBytes)
}
