// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package openstack provides a thin façade over gophercloud/v2.
//
// One authenticated ProviderClient is cached for the configured OVH project
// (set via OS_TENANT_ID or OS_TENANT_NAME). Service clients (Compute, Image,
// Network) are constructed per call against the requested region.
package openstack

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"

	"github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/config"
)

// Client is the project-scoped OpenStack façade used by the provisioner.
type Client struct {
	cfg config.OpenStackConfig

	mu sync.Mutex
	pc *gophercloud.ProviderClient
}

// New returns a Client; no Keystone calls are made until Provider is invoked.
func New(cfg config.OpenStackConfig) *Client {
	return &Client{cfg: cfg}
}

// ProjectKey returns a human-readable identifier for the configured project,
// useful for logs.
func (c *Client) ProjectKey() string {
	if c.cfg.ProjectID != "" {
		return "id:" + c.cfg.ProjectID
	}

	return "name:" + c.cfg.ProjectName
}

// Provider returns the project-scoped ProviderClient, authenticating on
// first use and caching the result.
func (c *Client) Provider(ctx context.Context) (*gophercloud.ProviderClient, error) {
	c.mu.Lock()
	pc := c.pc
	c.mu.Unlock()

	if pc != nil {
		return pc, nil
	}

	if c.cfg.ProjectID == "" && c.cfg.ProjectName == "" {
		return nil, errors.New("either project_id or project_name is required (set OS_TENANT_ID or OS_TENANT_NAME)")
	}

	scope := &gophercloud.AuthScope{}
	if c.cfg.ProjectID != "" {
		scope.ProjectID = c.cfg.ProjectID
	} else {
		scope.ProjectName = c.cfg.ProjectName
		scope.DomainName = c.cfg.ProjectDomainName
	}

	ao := gophercloud.AuthOptions{
		IdentityEndpoint: c.cfg.AuthURL,
		Username:         c.cfg.Username,
		Password:         c.cfg.Password,
		DomainName:       c.cfg.UserDomainName,
		Scope:            scope,
		AllowReauth:      true,
	}

	pc, err := openstack.AuthenticatedClient(ctx, ao)
	if err != nil {
		return nil, fmt.Errorf("openstack auth failed for project %q: %w", c.ProjectKey(), err)
	}

	c.mu.Lock()
	c.pc = pc
	c.mu.Unlock()

	return pc, nil
}

// Compute returns a Nova v2 service client scoped to region.
func (c *Client) Compute(pc *gophercloud.ProviderClient, region string) (*gophercloud.ServiceClient, error) {
	return openstack.NewComputeV2(pc, gophercloud.EndpointOpts{Region: region})
}

// Image returns a Glance v2 service client scoped to region.
func (c *Client) Image(pc *gophercloud.ProviderClient, region string) (*gophercloud.ServiceClient, error) {
	return openstack.NewImageV2(pc, gophercloud.EndpointOpts{Region: region})
}

// Network returns a Neutron v2 service client scoped to region.
func (c *Client) Network(pc *gophercloud.ProviderClient, region string) (*gophercloud.ServiceClient, error) {
	return openstack.NewNetworkV2(pc, gophercloud.EndpointOpts{Region: region})
}

// IsNotFound reports whether err is an OpenStack 404.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}

	return gophercloud.ResponseCodeIs(err, http.StatusNotFound)
}
