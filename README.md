# Omni Infrastructure Provider for OVHcloud

> :warning: This is **not** an official Sidero Labs provider. It is an
> independent, community-maintained project and is not affiliated with,
> endorsed by, or supported by Sidero Labs. :warning:

Can be used to automatically provision Talos nodes as instances on
OVHcloud Public Cloud via the standard OpenStack APIs (Keystone v3, Nova,
Glance, Neutron).

## Requirements

- An OVHcloud account with at least one Public Cloud project
- An OpenStack user with role on the target project (Public Cloud → Users
  & Roles → "OpenStack" credentials in the OVH Manager)
- An Omni account and an infrastructure provider key
- Network connectivity between the infrastructure provider and the OVHcloud
  OpenStack endpoint (default `https://auth.cloud.ovh.net/v3`)

One provider instance authenticates against a single OVHcloud Public Cloud
project (tenant). Region is supplied per `MachineClass` so the same
provider can spread workloads across multiple regions in that project.

## Running Infrastructure Provider

Create the configuration file for the provider:

```yaml
openstack:
  auth_url: https://auth.cloud.ovh.net/v3
  username: user-XXXXXXXX
  password: REPLACE_ME
  user_domain_name: Default
  project_domain_name: Default
  project_id: 3ed9c07512e94233b8ed3883f6cfe549   # or use project_name
```

All fields can be overridden by the matching environment variables (env
takes precedence):

| Env var                    | YAML field                      | Default   |
|----------------------------|---------------------------------|-----------|
| `OS_AUTH_URL`              | `openstack.auth_url`            | required  |
| `OS_USERNAME`              | `openstack.username`            | required  |
| `OS_PASSWORD`              | `openstack.password`            | required  |
| `OS_USER_DOMAIN_NAME`      | `openstack.user_domain_name`    | `Default` |
| `OS_PROJECT_DOMAIN_NAME`   | `openstack.project_domain_name` | `Default` |
| `OS_TENANT_ID`             | `openstack.project_id`          | one of these required |
| `OS_TENANT_NAME`           | `openstack.project_name`        | one of these required |

Env-var names match OVH's standard `openrc.sh`, so you can `source openrc.sh`
and run the provider directly.

### Using Docker

> **Note:** The `--omni-service-account-key` flag expects an *infra provider
> key*, not an Omni service account key. Make sure to provide the correct
> key type.

Build the image (no published image yet) and run it:

```bash
docker build -t omni-infra-provider-ovhcloud:dev .

docker run -it -d \
  -v ./config.yaml:/config.yaml \
  omni-infra-provider-ovhcloud:dev \
  --config-file /config.yaml \
  --omni-api-endpoint https://<account-name>.omni.siderolabs.io/ \
  --omni-service-account-key <infra-provider-key>
```

### Example Docker Compose

A `docker-compose.yml` is included that builds from source and reads
credentials from `.env`:

```bash
cp .env.example .env
$EDITOR .env       # fill in OVH_* and OMNI_*

make up            # rebuild image (no cache) + recreate container
make logs          # docker compose logs -f
make down          # stop + remove
```

## Creating a Machine Class for Auto Provision

To enable automatic provisioning of Talos nodes, define a machine class in
Omni that targets this provider via its ID (`ovhcloud`) and supplies the
OVHcloud-specific fields under `providerData`:

```yaml
metadata:
  name: ovh-gra11-b3-8
spec:
  matchLabels: {}
  infraProviderID: ovhcloud
  providerData: |
    region: GRA11
    flavor: b3-8
    network: Ext-Net
```

Apply the machine class with the Omni UI or CLI.

### `providerData` fields

| Field     | Type   | Required | Notes |
| --------- | ------ | -------- | ----- |
| `region`  | string | yes      | OVH region (`GRA11`, `SBG5`, `BHS5`, …) |
| `flavor`  | string | yes      | OpenStack flavor name (`b3-8`, `c3-8`, …) |
| `network` | string | yes      | Neutron network name or UUID. Use `Ext-Net` for the OVH public network, or your vRack/private network |

See [`test/machineclass.yaml`](test/machineclass.yaml) for a complete example.

### Looking up valid values

All of these come from your OVHcloud account. With OpenStack credentials
sourced (`source openrc.sh`):

| Field          | Command |
| -------------- | ------- |
| `project_id`   | `openstack project list` (or copy from the OVH Manager URL) |
| `region`       | `openstack region list` |
| `flavor`       | `openstack flavor list` |
| `network`      | `openstack network list` |

### Scaling a Cluster with the Machine Class

You can now use the `ovh-gra11-b3-8` machine class above to scale an
existing cluster up or down, or to create a new cluster:

- **To scale up:** Increase the desired number of machines in your cluster
  configuration. Omni will automatically provision new instances using the
  specified machine class.
- **To scale down:** Decrease the desired number of machines. Omni will
  remove excess instances accordingly.
- **To create a new cluster:** Specify the machine class in your cluster
  manifest when creating a new cluster.

Example cluster manifest snippet:

```yaml
spec:
  machineClass: ovh-gra11-b3-8
  replicas: 3
```

## Image handling

On first boot in a given `(project, region)`, the provider downloads the
Talos OpenStack image (qcow2) from
`factory.talos.dev/image/<schematic>/<version>/openstack-amd64.qcow2`,
uploads it to Glance with name
`talos-<short-schematic>-<version>-<region>`, and waits for it to become
`active`. Subsequent provisioning of the same `(project, region,
schematic, version)` reuses the cached image. Images are not deleted on
deprovision.

## Using Executable

Build the project (requires Go and `protoc`):

```bash
make generate     # protoc → api/specs/specs.pb.go
make build
```

Run the executable:

```bash
OS_AUTH_URL=https://auth.cloud.ovh.net/v3 \
OS_USERNAME=user-XXXXXXXX \
OS_PASSWORD=... \
OS_TENANT_ID=... \
  ./_out/omni-infra-provider-ovhcloud \
    --omni-api-endpoint https://<account-name>.omni.siderolabs.io/ \
    --omni-service-account-key <infra-provider-key>
```

## License

[MPL-2.0](LICENSE)
