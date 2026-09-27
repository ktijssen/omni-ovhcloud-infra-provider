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
// and network vary per MachineClass.
type Data struct {
	Region              string `yaml:"region"`
	Flavor              string `yaml:"flavor"`
	Network             string `yaml:"network"`
	InstanceGroupPolicy string `yaml:"instance_group_policy"`
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

	return nil
}
