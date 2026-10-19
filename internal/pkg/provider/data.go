// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider

import (
	"fmt"
	"slices"
	"strings"
)

// Data is the per-machine provider config supplied via the MachineClass.
//
// Project (tenant) is configured at the provider level; only region, flavor
// and networks vary per MachineClass.
type Data struct {
	Region              string   `yaml:"region"`
	Flavor              string   `yaml:"flavor"`
	Network             string   `yaml:"network"`
	InstanceGroupPolicy string   `yaml:"instance_group_policy"`
	AdditionalNetworks  []string `yaml:"additional_networks"`
}

// Networks returns every network to attach, in interface order: the primary
// network first, followed by the additional networks.
func (d *Data) Networks() []string {
	return append([]string{d.Network}, d.AdditionalNetworks...)
}

// Validate checks that the required fields are set.
func (d *Data) Validate() error {
	var missing []string

	if d.Region == "" {
		missing = append(missing, "region")
	}

	if d.Flavor == "" {
		missing = append(missing, "flavor")
	}

	if d.Network == "" {
		missing = append(missing, "network")
	}

	if len(missing) > 0 {
		return fmt.Errorf("required machine class field(s) missing: %s", strings.Join(missing, ", "))
	}

	if d.InstanceGroupPolicy != "" && !slices.Contains(supportedInstanceGroupPolicies, d.InstanceGroupPolicy) {
		return fmt.Errorf("unsupported instance_group_policy %q (supported: %s)",
			d.InstanceGroupPolicy, strings.Join(supportedInstanceGroupPolicies, ", "))
	}

	for i, ref := range d.AdditionalNetworks {
		if ref == "" {
			return fmt.Errorf("additional_networks[%d] is empty", i)
		}
	}

	return checkDuplicateNetworks(d.Networks())
}

// checkDuplicateNetworks rejects a network that is listed more than once.
func checkDuplicateNetworks(refs []string) error {
	seen := make(map[string]bool, len(refs))

	for _, ref := range refs {
		if seen[ref] {
			return fmt.Errorf("network %q is listed more than once", ref)
		}

		seen[ref] = true
	}

	return nil
}
