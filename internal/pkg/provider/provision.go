// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package provider implements the OVHcloud Public Cloud (OpenStack) infra provider core.
package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/imagedata"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/siderolabs/omni/client/pkg/constants"
	"github.com/siderolabs/omni/client/pkg/infra/provision"
	"github.com/siderolabs/omni/client/pkg/omni/resources/infra"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/stdpatches"
	"go.uber.org/zap"

	osfacade "github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider/openstack"
	"github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider/resources"
)

const (
	imageRetry    = 30 * time.Second
	instanceRetry = 10 * time.Second
	managedTag    = "omni-managed"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Provisioner provisions Talos nodes as OpenStack instances on OVHcloud Public Cloud.
type Provisioner struct {
	os *osfacade.Client

	mu          sync.Mutex
	flavorByKey map[string]string // (project|region|name) -> flavor ID
	netByKey    map[string]string // (project|region|name) -> network ID
}

// NewProvisioner creates a new provisioner.
func NewProvisioner(os *osfacade.Client) *Provisioner {
	return &Provisioner{
		os:          os,
		flavorByKey: map[string]string{},
		netByKey:    map[string]string{},
	}
}

// ProvisionSteps implements infra.Provisioner.
func (p *Provisioner) ProvisionSteps() []provision.Step[*resources.Machine] {
	return []provision.Step[*resources.Machine]{
		provision.NewStep("generateSchematic", p.stepGenerateSchematic),
		provision.NewStep("ensureImage", p.stepEnsureImage),
		provision.NewStep("createInstance", p.stepCreateInstance),
		provision.NewStep("waitInstance", p.stepWaitInstance),
	}
}

func (p *Provisioner) stepGenerateSchematic(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
	if pctx.State.TypedSpec().Value.Schematic != "" {
		return nil
	}

	schematic, err := pctx.GenerateSchematicID(ctx, logger, provision.WithoutConnectionParams())
	if err != nil {
		return fmt.Errorf("failed to generate schematic ID: %w", err)
	}

	pctx.State.TypedSpec().Value.Schematic = schematic
	pctx.State.TypedSpec().Value.TalosVersion = pctx.GetTalosVersion()

	logger.Info("schematic generated",
		zap.String("schematic", schematic),
		zap.String("talos_version", pctx.GetTalosVersion()))

	return nil
}

func (p *Provisioner) stepEnsureImage(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
	data, err := unmarshalData(pctx)
	if err != nil {
		return err
	}

	state := pctx.State.TypedSpec().Value
	state.Region = data.Region

	pc, err := p.os.Provider(ctx)
	if err != nil {
		return provision.NewRetryErrorf(imageRetry, "failed to authenticate to OpenStack: %w", err)
	}

	imageClient, err := p.os.Image(pc, data.Region)
	if err != nil {
		return fmt.Errorf("failed to build image client for region %q: %w", data.Region, err)
	}

	imageName := buildImageName(state.Schematic, state.TalosVersion, data.Region)

	if state.ImageId != "" {
		image, err := images.Get(ctx, imageClient, state.ImageId).Extract()
		if err != nil {
			if osfacade.IsNotFound(err) {
				logger.Info("cached image disappeared, will re-upload", zap.String("image_id", state.ImageId))

				state.ImageId = ""
			} else {
				return provision.NewRetryErrorf(imageRetry, "failed to get image %s: %w", state.ImageId, err)
			}
		} else {
			switch image.Status {
			case images.ImageStatusActive:
				logger.Info("image available", zap.String("image_id", image.ID), zap.String("name", image.Name))

				return nil
			case images.ImageStatusQueued, images.ImageStatusSaving, images.ImageStatusImporting:
				logger.Info("image still uploading", zap.String("image_id", image.ID), zap.String("status", string(image.Status)))

				return provision.NewRetryInterval(imageRetry)
			default:
				logger.Warn("image in unexpected state, re-uploading", zap.String("image_id", image.ID), zap.String("status", string(image.Status)))

				state.ImageId = ""
			}
		}
	}

	if existing, err := findImageByName(ctx, imageClient, imageName); err != nil {
		return provision.NewRetryErrorf(imageRetry, "failed to list images: %w", err)
	} else if existing != nil {
		state.ImageId = existing.ID

		if existing.Status == images.ImageStatusActive {
			logger.Info("found existing image", zap.String("image_id", existing.ID), zap.String("name", existing.Name))

			return nil
		}

		logger.Info("existing image still pending", zap.String("image_id", existing.ID), zap.String("status", string(existing.Status)))

		return provision.NewRetryInterval(imageRetry)
	}

	imageURL, err := buildImageURL(state.Schematic, state.TalosVersion)
	if err != nil {
		return fmt.Errorf("failed to build image URL: %w", err)
	}

	logger.Info("uploading custom image to Glance",
		zap.String("name", imageName),
		zap.String("region", data.Region),
		zap.String("url", imageURL))

	visibility := images.ImageVisibilityPrivate

	created, err := images.Create(ctx, imageClient, images.CreateOpts{
		Name:            imageName,
		DiskFormat:      "qcow2",
		ContainerFormat: "bare",
		Visibility:      &visibility,
		Tags:            []string{managedTag},
	}).Extract()
	if err != nil {
		return provision.NewRetryErrorf(imageRetry, "failed to create Glance image: %w", err)
	}

	state.ImageId = created.ID

	if err := uploadImage(ctx, imageClient, created.ID, imageURL); err != nil {
		return provision.NewRetryErrorf(imageRetry, "failed to upload image data: %w", err)
	}

	logger.Info("image data upload started", zap.String("image_id", created.ID))

	return provision.NewRetryInterval(imageRetry)
}

func (p *Provisioner) stepCreateInstance(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
	state := pctx.State.TypedSpec().Value
	if state.InstanceId != "" {
		return nil
	}

	if state.ImageId == "" {
		return provision.NewRetryErrorf(imageRetry, "waiting for image to be available")
	}

	data, err := unmarshalData(pctx)
	if err != nil {
		return err
	}

	pc, err := p.os.Provider(ctx)
	if err != nil {
		return provision.NewRetryErrorf(instanceRetry, "failed to authenticate to OpenStack: %w", err)
	}

	compute, err := p.os.Compute(pc, data.Region)
	if err != nil {
		return fmt.Errorf("failed to build compute client: %w", err)
	}

	flavorID, err := p.resolveFlavor(ctx, compute, p.os.ProjectKey(), data.Region, data.Flavor)
	if err != nil {
		return provision.NewRetryErrorf(instanceRetry, "failed to resolve flavor %q: %w", data.Flavor, err)
	}

	networkClient, err := p.os.Network(pc, data.Region)
	if err != nil {
		return fmt.Errorf("failed to build network client: %w", err)
	}

	networkID, err := p.resolveNetwork(ctx, networkClient, p.os.ProjectKey(), data.Region, data.Network)
	if err != nil {
		return provision.NewRetryErrorf(instanceRetry, "failed to resolve network %q: %w", data.Network, err)
	}

	hostname := pctx.GetRequestID()

	versionContract, err := talosconfig.ParseContractFromVersion(pctx.GetTalosVersion())
	if err != nil {
		return fmt.Errorf("failed to parse Talos contract from version %q: %w", pctx.GetTalosVersion(), err)
	}

	hostnamePatch, err := stdpatches.WithStaticHostname(versionContract, hostname)
	if err != nil {
		return fmt.Errorf("failed to build hostname config patch: %w", err)
	}

	if err = pctx.CreateConfigPatch(ctx, fmt.Sprintf("000-hostname-%s", hostname), hostnamePatch); err != nil {
		return provision.NewRetryErrorf(instanceRetry, "failed to create hostname config patch: %w", err)
	}

	logger.Info("creating Nova instance",
		zap.String("name", hostname),
		zap.String("region", data.Region),
		zap.String("flavor", data.Flavor),
		zap.String("flavor_id", flavorID),
		zap.String("network", data.Network),
		zap.String("network_id", networkID),
		zap.String("image_id", state.ImageId))

	server, err := servers.Create(ctx, compute, servers.CreateOpts{
		Name:      hostname,
		FlavorRef: flavorID,
		ImageRef:  state.ImageId,
		Networks:  []servers.Network{{UUID: networkID}},
		UserData:  []byte(pctx.ConnectionParams.JoinConfig),
		Metadata: map[string]string{
			"omni-managed": "true",
		},
	}, nil).Extract()
	if err != nil {
		return provision.NewRetryErrorf(instanceRetry, "failed to create instance: %w", err)
	}

	state.InstanceId = server.ID
	state.Region = data.Region

	pctx.SetMachineUUID(server.ID)

	logger.Info("instance creation started", zap.String("instance_id", server.ID))

	return provision.NewRetryInterval(instanceRetry)
}

func (p *Provisioner) stepWaitInstance(ctx context.Context, logger *zap.Logger, pctx provision.Context[*resources.Machine]) error {
	state := pctx.State.TypedSpec().Value
	if state.InstanceId == "" {
		return provision.NewRetryErrorf(instanceRetry, "instance not yet created")
	}

	data, err := unmarshalData(pctx)
	if err != nil {
		return err
	}

	pc, err := p.os.Provider(ctx)
	if err != nil {
		return provision.NewRetryErrorf(instanceRetry, "failed to authenticate to OpenStack: %w", err)
	}

	compute, err := p.os.Compute(pc, data.Region)
	if err != nil {
		return fmt.Errorf("failed to build compute client: %w", err)
	}

	server, err := servers.Get(ctx, compute, state.InstanceId).Extract()
	if err != nil {
		if osfacade.IsNotFound(err) {
			logger.Warn("instance vanished, clearing state", zap.String("instance_id", state.InstanceId))

			state.InstanceId = ""

			return provision.NewRetryErrorf(instanceRetry, "instance disappeared, will re-create")
		}

		return provision.NewRetryErrorf(instanceRetry, "failed to get instance: %w", err)
	}

	switch strings.ToUpper(server.Status) {
	case "ACTIVE":
		ip := extractIPv4(server.Addresses)
		state.PublicIpv4 = ip

		logger.Info("instance active",
			zap.String("instance_id", server.ID),
			zap.String("public_ipv4", ip))

		return nil
	case "BUILD", "REBUILD":
		logger.Info("instance still building",
			zap.String("instance_id", server.ID),
			zap.String("status", server.Status))

		return provision.NewRetryInterval(instanceRetry)
	case "ERROR":
		return fmt.Errorf("instance %s entered ERROR state: %v", server.ID, server.Fault)
	default:
		logger.Info("instance in transient state, polling",
			zap.String("instance_id", server.ID),
			zap.String("status", server.Status))

		return provision.NewRetryInterval(instanceRetry)
	}
}

// Deprovision implements infra.Provisioner.
//
// The Glance image is left in place — it is shared across all instances of
// the same (project, region, schematic, version) and image cleanup is out
// of scope for v1.
func (p *Provisioner) Deprovision(ctx context.Context, logger *zap.Logger, machine *resources.Machine, _ *infra.MachineRequest) error {
	state := machine.TypedSpec().Value

	if state.InstanceId == "" {
		logger.Info("no instance to delete")

		return nil
	}

	if state.Region == "" {
		logger.Warn("missing region in state; cannot deprovision",
			zap.String("instance_id", state.InstanceId))

		return fmt.Errorf("incomplete state for deprovision: region is empty")
	}

	pc, err := p.os.Provider(ctx)
	if err != nil {
		return fmt.Errorf("failed to authenticate to OpenStack: %w", err)
	}

	compute, err := p.os.Compute(pc, state.Region)
	if err != nil {
		return fmt.Errorf("failed to build compute client: %w", err)
	}

	logger.Info("deleting instance",
		zap.String("instance_id", state.InstanceId),
		zap.String("region", state.Region))

	if err := servers.Delete(ctx, compute, state.InstanceId).ExtractErr(); err != nil {
		if osfacade.IsNotFound(err) {
			logger.Info("instance already deleted", zap.String("instance_id", state.InstanceId))

			return nil
		}

		return fmt.Errorf("failed to delete instance %s: %w", state.InstanceId, err)
	}

	return nil
}

func unmarshalData(pctx provision.Context[*resources.Machine]) (Data, error) {
	var data Data

	if err := pctx.UnmarshalProviderData(&data); err != nil {
		return data, fmt.Errorf("failed to unmarshal provider data: %w", err)
	}

	if err := data.Validate(); err != nil {
		return data, err
	}

	return data, nil
}

func (p *Provisioner) resolveFlavor(ctx context.Context, compute *gophercloud.ServiceClient, projectKey, region, name string) (string, error) {
	key := projectKey + "|" + region + "|" + name

	p.mu.Lock()
	id, ok := p.flavorByKey[key]
	p.mu.Unlock()

	if ok {
		return id, nil
	}

	id, err := flavorIDFromName(ctx, compute, name)
	if err != nil {
		return "", err
	}

	p.mu.Lock()
	p.flavorByKey[key] = id
	p.mu.Unlock()

	return id, nil
}

func (p *Provisioner) resolveNetwork(ctx context.Context, network *gophercloud.ServiceClient, projectKey, region, ref string) (string, error) {
	if uuidRE.MatchString(ref) {
		return ref, nil
	}

	key := projectKey + "|" + region + "|" + ref

	p.mu.Lock()
	id, ok := p.netByKey[key]
	p.mu.Unlock()

	if ok {
		return id, nil
	}

	id, err := networkIDFromName(ctx, network, ref)
	if err != nil {
		return "", err
	}

	p.mu.Lock()
	p.netByKey[key] = id
	p.mu.Unlock()

	return id, nil
}

// flavorIDFromName lists Nova flavors and returns the ID of the one whose
// name matches exactly. gophercloud/v2 dropped the IDFromName helpers, so
// we match the v1 semantics here.
func flavorIDFromName(ctx context.Context, compute *gophercloud.ServiceClient, name string) (string, error) {
	var (
		id    string
		found int
	)

	err := flavors.ListDetail(compute, nil).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		list, err := flavors.ExtractFlavors(page)
		if err != nil {
			return false, err
		}

		for i := range list {
			if list[i].Name == name {
				id = list[i].ID
				found++
			}
		}

		return true, nil
	})
	if err != nil {
		return "", err
	}

	switch found {
	case 0:
		return "", fmt.Errorf("flavor %q not found", name)
	case 1:
		return id, nil
	default:
		return "", fmt.Errorf("found %d flavors named %q", found, name)
	}
}

// networkIDFromName resolves a Neutron network name to its UUID, server-side
// filtered by name (the OpenStack API supports it).
func networkIDFromName(ctx context.Context, network *gophercloud.ServiceClient, name string) (string, error) {
	var (
		id    string
		found int
	)

	err := networks.List(network, networks.ListOpts{Name: name}).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		list, err := networks.ExtractNetworks(page)
		if err != nil {
			return false, err
		}

		for i := range list {
			if list[i].Name == name {
				id = list[i].ID
				found++
			}
		}

		return true, nil
	})
	if err != nil {
		return "", err
	}

	switch found {
	case 0:
		return "", fmt.Errorf("network %q not found", name)
	case 1:
		return id, nil
	default:
		return "", fmt.Errorf("found %d networks named %q", found, name)
	}
}

// findImageByName lists Glance images filtered by exact name.
// Returns nil, nil if not found.
func findImageByName(ctx context.Context, imageClient *gophercloud.ServiceClient, name string) (*images.Image, error) {
	pages, err := images.List(imageClient, images.ListOpts{Name: name}).AllPages(ctx)
	if err != nil {
		return nil, err
	}

	list, err := images.ExtractImages(pages)
	if err != nil {
		return nil, err
	}

	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}

	return nil, nil
}

// uploadImage streams the image bytes from URL into Glance.
func uploadImage(ctx context.Context, imageClient *gophercloud.ServiceClient, imageID, imageURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return fmt.Errorf("build image request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download image %q: %w", imageURL, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download image %q: HTTP %d", imageURL, resp.StatusCode)
	}

	return imagedata.Upload(ctx, imageClient, imageID, resp.Body).ExtractErr()
}

func buildImageName(schematic, version, region string) string {
	short := schematic
	if len(short) > 12 {
		short = short[:12]
	}

	return fmt.Sprintf("talos-%s-%s-%s", short, sanitizeVersion(version), region)
}

func sanitizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	v = strings.ReplaceAll(v, "+", "-")

	return v
}

func buildImageURL(schematic, version string) (string, error) {
	u, err := url.Parse(constants.ImageFactoryBaseURL)
	if err != nil {
		return "", err
	}

	u = u.JoinPath("image", schematic, version, "openstack-amd64.qcow2")

	return u.String(), nil
}

// extractIPv4 returns the first public-looking IPv4 address from the server's
// Addresses map. OVHcloud Public Cloud attaches a public IP under the
// "Ext-Net" network by default; we prefer that, then fall back to the first
// IPv4 across all networks.
func extractIPv4(addrs map[string]any) string {
	const extNet = "Ext-Net"

	if v4 := firstIPv4InNetwork(addrs[extNet]); v4 != "" {
		return v4
	}

	for name, raw := range addrs {
		if name == extNet {
			continue
		}

		if v4 := firstIPv4InNetwork(raw); v4 != "" {
			return v4
		}
	}

	return ""
}

func firstIPv4InNetwork(raw any) string {
	list, ok := raw.([]any)
	if !ok {
		return ""
	}

	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}

		ver, _ := entry["version"].(float64)
		if int(ver) != 4 {
			continue
		}

		addr, _ := entry["addr"].(string)
		if addr != "" {
			return addr
		}
	}

	return ""
}
