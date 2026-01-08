package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

type Config interface {
	GetData() ConfigData
	AddRepository(repository GitRepository) error
	EditRepository(repository GitRepository) error
	RemoveRepository(alias string) error
	UseRepository(alias string) error
	SetUser(username, email string) error
	GetRepository(alias string) (GitRepository, error)
}

type UserInfo struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type GitRepository struct {
	Alias                string `yaml:"alias"`
	URL                  string `yaml:"url"`
	SSHKeyPath           string `yaml:"sshKeyPath"` // absolute path to SSH key file
	PGPKeyPath           string `yaml:"pgpKeyPath"` // absolute path to PGP key file
	GovernancePublicKey  string `yaml:"governancePublicKey"`
	GovernanceFolderPath string `yaml:"governanceFolderPath"`
	MSRName              string `yaml:"msrName"`
	MCAName              string `yaml:"mcaName"`
}

type ConfigData struct {
	User              UserInfo        `yaml:"user"`
	CurrentRepository string          `yaml:"currentRepository"`
	Repositories      []GitRepository `yaml:"repositories"`
}

func (cd *ConfigData) StringYaml() string {
	out, err := yaml.Marshal(cd)
	if err != nil {
		log.Fatal(err)
	}

	return string(out)
}

type config struct {
	Data     ConfigData
	FilePath string
}

const (
	DefaultConfigPath     = "/.qubmango/"
	DefaultConfigFileName = "config.yaml"
)

func LoadConfig() (Config, error) {
	return LoadConfigWithPath(DefaultConfigPath + DefaultConfigFileName)
}

func LoadConfigWithPath(
	configPath string,
) (Config, error) {
	// Config file requires yaml format
	if !strings.HasSuffix(configPath, ".yaml") {
		return nil, fmt.Errorf("config file supports only .yaml format")
	}

	p := filepath.Join(filepath.Clean(configPath))
	if _, err := os.Stat(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Unknown error
		return nil, fmt.Errorf("fetch config file from location %s: %w", configPath, err)
	} else if err != nil && errors.Is(err, os.ErrNotExist) {
		// Config file doesn't exist. Create empty config yaml file
		if err := os.MkdirAll(filepath.Dir(p), 0644); err != nil {
			return nil, fmt.Errorf("create directory %s for config file: %w", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, nil, 0644); err != nil {
			return nil, fmt.Errorf("create config file %s: %w", p, err)
		}
	}

	// Get file and extract data from it
	file, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", p, err)
	}

	var data ConfigData
	if err := yaml.Unmarshal([]byte(file), &data); err != nil {
		return nil, fmt.Errorf("could not unmarshal config file from %s: %w", p, err)
	}

	result := &config{
		Data:     data,
		FilePath: p,
	}
	return result, nil
}

func (c *config) GetData() ConfigData {
	return c.Data
}

func (c *config) AddRepository(
	repository GitRepository,
) error {
	// Check if alias already exists
	exist, _ := c.checkAliasExists(repository.Alias)
	if exist {
		return fmt.Errorf("repository with alias %s already exists", repository.Alias)
	}

	// Append new repository to config data
	c.Data.Repositories = append(c.Data.Repositories, repository)
	return c.saveData()
}

func (c *config) EditRepository(
	repository GitRepository,
) error {
	// Check if alias already exists
	exist, idx := c.checkAliasExists(repository.Alias)
	if !exist {
		return fmt.Errorf("repository with alias %s does not exist", repository.Alias)
	}

	src := c.Data.Repositories[idx]
	repository = defaulted(src, repository)

	// Update existing repository with new values
	c.Data.Repositories[idx] = defaulted(src, repository)
	return c.saveData()
}

func defaulted(
	src, dst GitRepository,
) GitRepository {
	if dst.Alias == "" {
		dst.Alias = src.Alias
	}
	if dst.URL == "" {
		dst.URL = src.URL
	}
	if dst.SSHKeyPath == "" {
		dst.SSHKeyPath = src.SSHKeyPath
	}
	if dst.PGPKeyPath == "" {
		dst.PGPKeyPath = src.PGPKeyPath
	}
	if dst.GovernancePublicKey == "" {
		dst.GovernancePublicKey = src.GovernancePublicKey
	}
	if dst.GovernanceFolderPath == "" {
		dst.GovernanceFolderPath = src.GovernanceFolderPath
	}
	if dst.MCAName == "" {
		dst.MCAName = src.MCAName
	}
	if dst.MSRName == "" {
		dst.MSRName = src.MSRName
	}

	return dst
}

func (c *config) RemoveRepository(
	alias string,
) error {
	// Check if alias already exists
	exist, idx := c.checkAliasExists(alias)
	if !exist {
		return fmt.Errorf("repository with alias %s does not exist", alias)
	}

	// Remove existing repository
	c.Data.Repositories = append(c.Data.Repositories[:idx], c.Data.Repositories[idx+1:]...)

	// If alias is currently used repository - remove it
	if alias == c.Data.CurrentRepository {
		c.Data.CurrentRepository = ""
	}

	return c.saveData()
}

func (c *config) UseRepository(
	alias string,
) error {
	// Check if alias already exists (empty alias is allowed to unset current repo)
	if alias != "" {
		exist, _ := c.checkAliasExists(alias)
		if !exist {
			return fmt.Errorf("repository with alias %s does not exist", alias)
		}
	}

	c.Data.CurrentRepository = alias
	return c.saveData()
}

func (c *config) SetUser(
	username, email string,
) error {
	c.Data.User = UserInfo{
		Name:  username,
		Email: email,
	}

	return c.saveData()
}

func (c *config) saveData() error {
	// Convert config data back to yaml and write to file
	dataFile, err := yaml.Marshal(c.Data)
	if err != nil {
		return fmt.Errorf("could not marshal updated config data: %w", err)
	}
	if err := os.WriteFile(c.FilePath, dataFile, 0644); err != nil {
		return fmt.Errorf("could not write updated config to file %s: %w", c.FilePath, err)
	}

	return nil
}

func (c *config) checkAliasExists(
	alias string,
) (bool, int) {
	exist := false
	idx := -1
	for i, repo := range c.Data.Repositories {
		if repo.Alias == alias {
			exist = true
			idx = i
			break
		}
	}

	return exist, idx
}

func (c *config) GetRepository(
	alias string,
) (GitRepository, error) {
	// Check if alias already exists
	exist, idx := c.checkAliasExists(alias)
	if !exist {
		return GitRepository{}, fmt.Errorf("repository with alias %s does not exist", alias)
	}

	return c.Data.Repositories[idx], nil
}
