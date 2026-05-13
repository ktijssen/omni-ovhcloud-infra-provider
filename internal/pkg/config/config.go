// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package config contains configuration types and validation for the ovhcloud provider.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config describes ovhcloud provider configuration.
type Config struct {
	OpenStack OpenStackConfig `yaml:"openstack"`
}

// OpenStackConfig holds the OpenStack credentials and project (tenant) scope.
//
// One provider instance authenticates against a single OVHcloud Public Cloud
// project. Region is supplied per-MachineClass under providerData.
type OpenStackConfig struct {
	AuthURL           string `yaml:"auth_url"`
	Username          string `yaml:"username"`
	Password          string `yaml:"password"`
	UserDomainName    string `yaml:"user_domain_name"`
	ProjectDomainName string `yaml:"project_domain_name"`
	ProjectID         string `yaml:"project_id,omitempty"`
	ProjectName       string `yaml:"project_name,omitempty"`
}

// Load reads configuration from the given YAML file (if non-empty) and
// applies environment-variable overrides for credentials.
//
// All env vars use the OVH_ prefix. Env vars take precedence over the file.
func Load(path string) (*Config, error) {
	cfg := &Config{}

	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("failed to open config file %q: %w", path, err)
		}

		defer f.Close()

		if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file %q: %w", path, err)
		}
	}

	if v := os.Getenv("OS_AUTH_URL"); v != "" {
		cfg.OpenStack.AuthURL = v
	}

	if v := os.Getenv("OS_USERNAME"); v != "" {
		cfg.OpenStack.Username = v
	}

	if v := os.Getenv("OS_PASSWORD"); v != "" {
		cfg.OpenStack.Password = v
	}

	if v := os.Getenv("OS_USER_DOMAIN_NAME"); v != "" {
		cfg.OpenStack.UserDomainName = v
	}

	if v := os.Getenv("OS_PROJECT_DOMAIN_NAME"); v != "" {
		cfg.OpenStack.ProjectDomainName = v
	}

	if v := os.Getenv("OS_TENANT_ID"); v != "" {
		cfg.OpenStack.ProjectID = v
	}

	if v := os.Getenv("OS_TENANT_NAME"); v != "" {
		cfg.OpenStack.ProjectName = v
	}

	if cfg.OpenStack.UserDomainName == "" {
		cfg.OpenStack.UserDomainName = "Default"
	}

	if cfg.OpenStack.ProjectDomainName == "" {
		cfg.OpenStack.ProjectDomainName = "Default"
	}

	return cfg, nil
}

// Validate checks that all required fields are set.
func (c *Config) Validate() error {
	var missing []string

	if c.OpenStack.AuthURL == "" {
		missing = append(missing, "openstack.auth_url (OS_AUTH_URL)")
	}

	if c.OpenStack.Username == "" {
		missing = append(missing, "openstack.username (OS_USERNAME)")
	}

	if c.OpenStack.Password == "" {
		missing = append(missing, "openstack.password (OS_PASSWORD)")
	}

	if c.OpenStack.ProjectID == "" && c.OpenStack.ProjectName == "" {
		missing = append(missing, "openstack.project_id or openstack.project_name (OS_TENANT_ID or OS_TENANT_NAME)")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %v", missing)
	}

	return nil
}
