package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const defaultBindAddress = "0.0.0.0:9234"

//go:embed default.yaml
var defaultConfigYAML []byte

type Config struct {
	BindAddress  string            `yaml:"bind_address"`
	ResetMetrics *bool             `yaml:"reset_metrics"`
	Groups       []MQTTGroupConfig `yaml:"groups"`
}

type MQTTGroupConfig struct {
	Name     string `yaml:"name"`
	Endpoint string `yaml:"endpoint"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	User     string `yaml:"user"`
	Pass     string `yaml:"pass"`
	Cert     string `yaml:"cert"`
	Key      string `yaml:"key"`
	ClientID string `yaml:"client_id"`
}

func loadConfig(path string) (Config, error) {
	data := defaultConfigYAML
	if path != "" {
		override, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %q: %w", path, err)
		}
		data, err = mergeYAML(defaultConfigYAML, override)
		if err != nil {
			return Config{}, fmt.Errorf("merge config %q: %w", path, err)
		}
	}

	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	if config.BindAddress == "" {
		config.BindAddress = defaultBindAddress
	}
	for index := range config.Groups {
		group := &config.Groups[index]
		if group.Username == "" {
			group.Username = group.User
		}
		if group.Password == "" {
			group.Password = group.Pass
		}
	}
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func mergeYAML(defaultData, overrideData []byte) ([]byte, error) {
	var defaultDocument, overrideDocument yaml.Node
	if err := yaml.Unmarshal(defaultData, &defaultDocument); err != nil {
		return nil, fmt.Errorf("parse embedded default config: %w", err)
	}
	if err := yaml.Unmarshal(overrideData, &overrideDocument); err != nil {
		return nil, fmt.Errorf("parse override config: %w", err)
	}
	if len(defaultDocument.Content) == 0 || len(overrideDocument.Content) == 0 {
		return nil, errors.New("config must contain a YAML document")
	}

	mergeYAMLNodes(defaultDocument.Content[0], overrideDocument.Content[0])
	var merged bytes.Buffer
	encoder := yaml.NewEncoder(&merged)
	if err := encoder.Encode(&defaultDocument); err != nil {
		return nil, fmt.Errorf("encode merged config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close merged config encoder: %w", err)
	}
	return merged.Bytes(), nil
}

func mergeYAMLNodes(defaultNode, overrideNode *yaml.Node) {
	if defaultNode.Kind != yaml.MappingNode || overrideNode.Kind != yaml.MappingNode {
		*defaultNode = *overrideNode
		return
	}

	for index := 0; index < len(overrideNode.Content); index += 2 {
		overrideKey := overrideNode.Content[index]
		overrideValue := overrideNode.Content[index+1]
		defaultValue := findYAMLMapValue(defaultNode, overrideKey.Value)
		if defaultValue == nil {
			defaultNode.Content = append(defaultNode.Content, overrideKey, overrideValue)
			continue
		}
		mergeYAMLNodes(defaultValue, overrideValue)
	}
}

func findYAMLMapValue(node *yaml.Node, key string) *yaml.Node {
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func validateConfig(config Config) error {
	if len(config.Groups) == 0 {
		return errors.New("config must define at least one MQTT group")
	}

	seen := make(map[string]struct{}, len(config.Groups))
	for index, group := range config.Groups {
		if group.Name == "" {
			return fmt.Errorf("groups[%d].name must not be empty", index)
		}
		if _, exists := seen[group.Name]; exists {
			return fmt.Errorf("duplicate MQTT group name %q", group.Name)
		}
		seen[group.Name] = struct{}{}
		if group.Endpoint == "" {
			return fmt.Errorf("groups[%d].endpoint must not be empty", index)
		}
		if (group.Cert == "") != (group.Key == "") {
			return fmt.Errorf("groups[%d] must define both cert and key for TLS client authentication", index)
		}
	}
	return nil
}

func (c Config) shouldResetMetrics() bool {
	if c.ResetMetrics == nil {
		return true
	}
	return *c.ResetMetrics
}
