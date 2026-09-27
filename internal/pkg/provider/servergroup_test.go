// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider"
)

type fakeGroup struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Policies []string `json:"policies"`
	Members  []string `json:"members"`
}

type fakeServer struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	TaskState string `json:"OS-EXT-STS:task_state,omitempty"`
}

// fakeNova is a minimal in-memory implementation of the Nova
// os-server-groups API, plus server lookups.
type fakeNova struct {
	groups  map[string]*fakeGroup
	servers map[string]*fakeServer
	creates []map[string]any
	deletes []string
	nextID  int
	mu      sync.Mutex
}

func newFakeNova(t *testing.T, groups ...*fakeGroup) (*fakeNova, *gophercloud.ServiceClient) {
	f := &fakeNova{groups: map[string]*fakeGroup{}, servers: map[string]*fakeServer{}}

	for _, g := range groups {
		f.groups[g.ID] = g
	}

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	return f, &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       srv.URL + "/",
	}
}

func (f *fakeNova) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	id, hasID := strings.CutPrefix(r.URL.Path, "/os-server-groups/")
	serverID, hasServerID := strings.CutPrefix(r.URL.Path, "/servers/")

	switch {
	case r.Method == http.MethodGet && hasServerID:
		s, ok := f.servers[serverID]
		if !ok {
			http.NotFound(w, r)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"server": s})
	case r.Method == http.MethodGet && r.URL.Path == "/os-server-groups":
		list := make([]*fakeGroup, 0, len(f.groups))
		for _, g := range f.groups {
			list = append(list, g)
		}

		writeJSON(w, http.StatusOK, map[string]any{"server_groups": list})
	case r.Method == http.MethodPost && r.URL.Path == "/os-server-groups":
		var body struct {
			ServerGroup map[string]any `json:"server_group"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		f.creates = append(f.creates, body.ServerGroup)
		f.nextID++

		g := &fakeGroup{ID: fmt.Sprintf("created-%d", f.nextID), Members: []string{}}
		g.Name, _ = body.ServerGroup["name"].(string) //nolint:errcheck // test fake; missing name surfaces as an empty name

		if policies, ok := body.ServerGroup["policies"].([]any); ok {
			for _, p := range policies {
				if s, ok := p.(string); ok {
					g.Policies = append(g.Policies, s)
				}
			}
		}

		f.groups[g.ID] = g

		writeJSON(w, http.StatusOK, map[string]any{"server_group": g})
	case r.Method == http.MethodGet && hasID:
		g, ok := f.groups[id]
		if !ok {
			http.NotFound(w, r)

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"server_group": g})
	case r.Method == http.MethodDelete && hasID:
		if _, ok := f.groups[id]; !ok {
			http.NotFound(w, r)

			return
		}

		delete(f.groups, id)
		f.deletes = append(f.deletes, id)

		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusNotImplemented)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck,errchkjson // test fake
}

func TestServerGroupName(t *testing.T) {
	if got, want := provider.ServerGroupName("my-cluster-control-planes"), "omni-my-cluster-control-planes"; got != want {
		t.Fatalf("ServerGroupName() = %q, want %q", got, want)
	}
}

func TestEnsureServerGroupCreates(t *testing.T) {
	fake, compute := newFakeNova(t, &fakeGroup{ID: "other", Name: "unrelated", Policies: []string{"anti-affinity"}})

	id, err := provider.EnsureServerGroup(t.Context(), zap.NewNop(), compute, "omni-set", "anti-affinity")
	if err != nil {
		t.Fatalf("EnsureServerGroup() error: %v", err)
	}

	if len(fake.creates) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(fake.creates))
	}

	req := fake.creates[0]
	if req["name"] != "omni-set" {
		t.Errorf("created group name = %v, want omni-set", req["name"])
	}

	if policies, _ := req["policies"].([]any); len(policies) != 1 || policies[0] != "anti-affinity" { //nolint:errcheck // nil on mismatch fails the check
		t.Errorf("created group policies = %v, want [anti-affinity]", req["policies"])
	}

	// A second call must find the group created by the first one.
	again, err := provider.EnsureServerGroup(t.Context(), zap.NewNop(), compute, "omni-set", "anti-affinity")
	if err != nil {
		t.Fatalf("second EnsureServerGroup() error: %v", err)
	}

	if again != id {
		t.Errorf("second EnsureServerGroup() = %q, want %q", again, id)
	}

	if len(fake.creates) != 1 {
		t.Errorf("expected no additional create calls, got %d total", len(fake.creates))
	}
}

func TestEnsureServerGroupReusesExisting(t *testing.T) {
	fake, compute := newFakeNova(t, &fakeGroup{ID: "g1", Name: "omni-set", Policies: []string{"anti-affinity"}})

	id, err := provider.EnsureServerGroup(t.Context(), zap.NewNop(), compute, "omni-set", "anti-affinity")
	if err != nil {
		t.Fatalf("EnsureServerGroup() error: %v", err)
	}

	if id != "g1" {
		t.Errorf("EnsureServerGroup() = %q, want g1", id)
	}

	if len(fake.creates) != 0 {
		t.Errorf("expected no create calls, got %d", len(fake.creates))
	}
}

func TestEnsureServerGroupErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wantErr string
		groups  []*fakeGroup
	}{
		{
			name:    "wrong policy",
			groups:  []*fakeGroup{{ID: "g1", Name: "omni-set", Policies: []string{"affinity"}}},
			wantErr: `does not have the "anti-affinity" policy`,
		},
		{
			name: "duplicate names",
			groups: []*fakeGroup{
				{ID: "g1", Name: "omni-set", Policies: []string{"anti-affinity"}},
				{ID: "g2", Name: "omni-set", Policies: []string{"anti-affinity"}},
			},
			wantErr: `found 2 server groups named "omni-set"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, compute := newFakeNova(t, tc.groups...)

			_, err := provider.EnsureServerGroup(t.Context(), zap.NewNop(), compute, "omni-set", "anti-affinity")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("EnsureServerGroup() error = %v, want containing %q", err, tc.wantErr)
			}

			if len(fake.creates) != 0 {
				t.Errorf("expected no create calls, got %d", len(fake.creates))
			}
		})
	}
}

func TestCleanupServerGroup(t *testing.T) {
	for _, tc := range []struct {
		name        string
		instanceID  string
		members     []string
		servers     []*fakeServer
		wantDeleted bool
	}{
		{name: "only the deprovisioned instance", members: []string{"i1"}, instanceID: "i1", wantDeleted: true},
		{name: "already empty", members: []string{}, instanceID: "i1", wantDeleted: true},
		{name: "instance never created", members: []string{}, instanceID: "", wantDeleted: true},
		{
			name: "other members remain", members: []string{"i1", "i2"}, instanceID: "i1",
			servers: []*fakeServer{{ID: "i2", Status: "ACTIVE"}}, wantDeleted: false,
		},
		{
			name: "unrelated member", members: []string{"i2"}, instanceID: "i1",
			servers: []*fakeServer{{ID: "i2", Status: "ACTIVE"}}, wantDeleted: false,
		},
		{
			name: "other members being deleted", members: []string{"i1", "i2", "i3", "i4"}, instanceID: "i3",
			servers: []*fakeServer{
				{ID: "i1", Status: "ACTIVE", TaskState: "deleting"},
				{ID: "i4", Status: "DELETED"},
			},
			wantDeleted: true,
		},
		{
			name: "one live member among deleted", members: []string{"i1", "i2", "i3"}, instanceID: "i3",
			servers: []*fakeServer{
				{ID: "i1", Status: "ACTIVE", TaskState: "deleting"},
				{ID: "i2", Status: "ACTIVE"},
			},
			wantDeleted: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, compute := newFakeNova(t, &fakeGroup{ID: "g1", Name: "omni-set", Policies: []string{"anti-affinity"}, Members: tc.members})

			for _, s := range tc.servers {
				fake.servers[s.ID] = s
			}

			if err := provider.CleanupServerGroup(t.Context(), zap.NewNop(), compute, "g1", tc.instanceID); err != nil {
				t.Fatalf("CleanupServerGroup() error: %v", err)
			}

			_, exists := fake.groups["g1"]
			if exists == tc.wantDeleted {
				t.Errorf("group exists = %v, want deleted = %v", exists, tc.wantDeleted)
			}
		})
	}
}

func TestCleanupServerGroupMissing(t *testing.T) {
	fake, compute := newFakeNova(t)

	if err := provider.CleanupServerGroup(t.Context(), zap.NewNop(), compute, "gone", "i1"); err != nil {
		t.Fatalf("CleanupServerGroup() on missing group error: %v", err)
	}

	if len(fake.deletes) != 0 {
		t.Errorf("expected no delete calls, got %v", fake.deletes)
	}
}

func TestDataInstanceGroupPolicy(t *testing.T) {
	const base = "region: GRA11\nflavor: b3-8\nnetwork: Ext-Net\n"

	for _, tc := range []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "unset", in: base, want: ""},
		{name: "anti-affinity", in: base + "instance_group_policy: anti-affinity\n", want: "anti-affinity"},
		{name: "affinity unsupported", in: base + "instance_group_policy: affinity\n", wantErr: `unsupported instance_group_policy "affinity"`},
		{name: "unknown", in: base + "instance_group_policy: spread\n", wantErr: `unsupported instance_group_policy "spread" (supported: anti-affinity)`},
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

			if data.InstanceGroupPolicy != tc.want {
				t.Errorf("InstanceGroupPolicy = %q, want %q", data.InstanceGroupPolicy, tc.want)
			}
		})
	}
}
