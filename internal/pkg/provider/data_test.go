// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider_test

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider"
)

func TestDataAdditionalNetworks(t *testing.T) {
	const base = "region: GRA11\nflavor: b3-8\nnetwork: Ext-Net\n"

	for _, tc := range []struct {
		name    string
		in      string
		wantErr string
		want    []string
	}{
		{name: "unset", in: base, want: []string{"Ext-Net"}},
		{name: "empty list", in: base + "additional_networks: []\n", want: []string{"Ext-Net"}},
		{
			name: "ordered",
			in:   base + "additional_networks:\n  - vrack\n  - 0b6a5f3e-1c2d-4e5f-8a9b-0c1d2e3f4a5b\n",
			want: []string{"Ext-Net", "vrack", "0b6a5f3e-1c2d-4e5f-8a9b-0c1d2e3f4a5b"},
		},
		{name: "empty entry", in: base + "additional_networks:\n  - vrack\n  - \"\"\n", wantErr: "additional_networks[1] is empty"},
		{name: "duplicate of primary", in: base + "additional_networks:\n  - Ext-Net\n", wantErr: `network "Ext-Net" is listed more than once`},
		{name: "duplicate entry", in: base + "additional_networks:\n  - vrack\n  - vrack\n", wantErr: `network "vrack" is listed more than once`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data provider.Data

			if err := yaml.Unmarshal([]byte(tc.in), &data); err != nil {
				t.Fatalf("yaml.Unmarshal() error: %v", err)
			}

			err := data.Validate()

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Validate() error = %v, want containing %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Validate() error: %v", err)
			}

			if got := data.Networks(); !slices.Equal(got, tc.want) {
				t.Errorf("Networks() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractIPv4(t *testing.T) {
	addr := func(ips ...string) []any {
		list := make([]any, 0, 1+len(ips))
		list = append(list, map[string]any{"version": float64(6), "addr": "2001:db8::1"})

		for _, ip := range ips {
			list = append(list, map[string]any{"version": float64(4), "addr": ip})
		}

		return list
	}

	for _, tc := range []struct {
		addrs    map[string]any
		name     string
		want     string
		networks []string
	}{
		{
			name:     "ext-net preferred",
			addrs:    map[string]any{"vrack": addr("10.0.0.5"), "Ext-Net": addr("51.0.0.1")},
			networks: []string{"vrack", "Ext-Net"},
			want:     "51.0.0.1",
		},
		{
			name:     "configured order",
			addrs:    map[string]any{"a-net": addr("10.0.1.5"), "z-net": addr("10.0.2.5")},
			networks: []string{"z-net", "a-net"},
			want:     "10.0.2.5",
		},
		{
			name:     "name order fallback",
			addrs:    map[string]any{"z-net": addr("10.0.2.5"), "a-net": addr("10.0.1.5")},
			networks: []string{"0b6a5f3e-1c2d-4e5f-8a9b-0c1d2e3f4a5b"},
			want:     "10.0.1.5",
		},
		{
			name:     "skips networks without ipv4",
			addrs:    map[string]any{"v6-only": addr(), "vrack": addr("10.0.0.5")},
			networks: []string{"v6-only", "vrack"},
			want:     "10.0.0.5",
		},
		{name: "none", addrs: map[string]any{}, networks: []string{"Ext-Net"}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := provider.ExtractIPv4(tc.addrs, tc.networks); got != tc.want {
				t.Errorf("ExtractIPv4() = %q, want %q", got, tc.want)
			}
		})
	}
}
