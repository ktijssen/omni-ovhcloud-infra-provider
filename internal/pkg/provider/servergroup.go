// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servergroups"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"go.uber.org/zap"

	osfacade "github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider/openstack"
)

// supportedInstanceGroupPolicies lists the values accepted for
// instance_group_policy. OVHcloud instance group types map directly onto
// Nova server group policies.
var supportedInstanceGroupPolicies = []string{"anti-affinity"}

// serverGroupName returns the name of the provider-managed server group for
// a machine request set.
func serverGroupName(machineRequestSetID string) string {
	return "omni-" + machineRequestSetID
}

// ensureServerGroup returns the ID of the server group named name with the
// given policy, creating it if it does not exist.
//
// OVHcloud instance groups are Nova server groups, so they are managed
// through the compute API rather than the OVH API. The caller must hold
// p.groupMu so that concurrent provisions for the same machine request set
// do not create duplicate groups.
func ensureServerGroup(ctx context.Context, logger *zap.Logger, compute *gophercloud.ServiceClient, name, policy string) (string, error) {
	existing, err := findServerGroupByName(ctx, compute, name)
	if err != nil {
		return "", fmt.Errorf("failed to list server groups: %w", err)
	}

	if existing != nil {
		if !hasPolicy(existing, policy) {
			return "", fmt.Errorf("server group %q (%s) exists but does not have the %q policy", name, existing.ID, policy)
		}

		return existing.ID, nil
	}

	created, err := servergroups.Create(ctx, compute, servergroups.CreateOpts{
		Name:     name,
		Policies: []string{policy},
	}).Extract()
	if err != nil {
		return "", fmt.Errorf("failed to create server group %q: %w", name, err)
	}

	logger.Info("created server group",
		zap.String("server_group", name),
		zap.String("policy", policy),
		zap.String("server_group_id", created.ID))

	return created.ID, nil
}

// cleanupServerGroup deletes the server group if no live members other than
// instanceID remain. Nova removes a deleted server from its group
// asynchronously, so the instance being deprovisioned, as well as other
// members whose deletion is still in progress, may still be listed; such
// members are not counted.
//
// The caller must hold p.groupMu.
func cleanupServerGroup(ctx context.Context, logger *zap.Logger, compute *gophercloud.ServiceClient, groupID, instanceID string) error {
	group, err := servergroups.Get(ctx, compute, groupID).Extract()
	if err != nil {
		if osfacade.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to get server group %s: %w", groupID, err)
	}

	for _, member := range group.Members {
		if member == instanceID {
			continue
		}

		alive, aliveErr := isServerAlive(ctx, compute, member)
		if aliveErr != nil {
			return fmt.Errorf("failed to check server group %s member %s: %w", groupID, member, aliveErr)
		}

		if alive {
			logger.Info("server group still has members, keeping it",
				zap.String("server_group_id", groupID),
				zap.String("member", member),
				zap.Int("members", len(group.Members)))

			return nil
		}
	}

	if err = servergroups.Delete(ctx, compute, groupID).ExtractErr(); err != nil && !osfacade.IsNotFound(err) {
		return fmt.Errorf("failed to delete server group %s: %w", groupID, err)
	}

	logger.Info("deleted empty server group",
		zap.String("server_group", group.Name),
		zap.String("server_group_id", groupID))

	return nil
}

// isServerAlive reports whether the server exists and is not being deleted.
func isServerAlive(ctx context.Context, compute *gophercloud.ServiceClient, id string) (bool, error) {
	server, err := servers.Get(ctx, compute, id).Extract()
	if err != nil {
		if osfacade.IsNotFound(err) {
			return false, nil
		}

		return false, err
	}

	switch {
	case server.Status == "DELETED", server.Status == "SOFT_DELETED", server.TaskState == "deleting":
		return false, nil
	default:
		return true, nil
	}
}

// findServerGroupByName returns the server group with the given name.
// Returns nil, nil if not found.
func findServerGroupByName(ctx context.Context, compute *gophercloud.ServiceClient, name string) (*servergroups.ServerGroup, error) {
	var (
		found *servergroups.ServerGroup
		count int
	)

	err := servergroups.List(compute, nil).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		list, err := servergroups.ExtractServerGroups(page)
		if err != nil {
			return false, err
		}

		for i := range list {
			if list[i].Name == name {
				found = &list[i]
				count++
			}
		}

		return true, nil
	})
	if err != nil {
		return nil, err
	}

	if count > 1 {
		return nil, fmt.Errorf("found %d server groups named %q", count, name)
	}

	return found, nil
}

func hasPolicy(group *servergroups.ServerGroup, policy string) bool {
	return (group.Policy != nil && *group.Policy == policy) || slices.Contains(group.Policies, policy)
}
