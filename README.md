# Autoscaler

Scale your woodpecker agents automatically to the moon and back based on the current load.

## Usage

If you are using docker-compose you can add the following to your `docker-compose.yml` file:

```yml
# docker-compose.yml
version: '3'

services:
  woodpecker-server:
    image: woodpeckerci/woodpecker-server:next
    [...]

  woodpecker-autoscaler:
    image: woodpeckerci/autoscaler:next
    restart: always
    depends_on:
      - woodpecker-server
    environment:
      - WOODPECKER_SERVER=https://your-woodpecker-server.tld # the url of your woodpecker server / could also be a public url
      - WOODPECKER_TOKEN=${WOODPECKER_TOKEN} # the Personal Access Token you can get from the UI https://your-woodpecker-server.tld/user/cli-and-api
      - WOODPECKER_MIN_AGENTS=0
      - WOODPECKER_MAX_AGENTS=3
      - WOODPECKER_WORKFLOWS_PER_AGENT=2 # the number of workflows each agent can run at the same time
      - WOODPECKER_GRPC_ADDR=https://grpc.your-woodpecker-server.tld # the grpc address of your woodpecker server, publicly accessible from the agents
      - WOODPECKER_GRPC_SECURE=true
      - WOODPECKER_AGENT_ENV= # optional environment variables to pass to the agents
      - WOODPECKER_PROVIDER=hetznercloud # set the provider, you can find all the available ones down below
      - WOODPECKER_HETZNERCLOUD_API_TOKEN=${WOODPECKER_HETZNERCLOUD_API_TOKEN} # your api token for the Hetzner cloud
```

The agents will use `WOODPECKER_GRPC_ADDR` and an agent token automatically created on the server by the autoscaler to connect to the server. Therefore the `WOODPECKER_GRPC_ADDR` has to be publicly accessible from the newly created agents. Check for example how you could use [caddy](https://woodpecker-ci.org/docs/administration/configuration/server#caddy) to expose the grpc connection.

## Equinix Metal

Set `WOODPECKER_PROVIDER=equinixmetal` and configure at least:

- `WOODPECKER_EQUINIXMETAL_API_TOKEN`
- `WOODPECKER_EQUINIXMETAL_PROJECT_ID`
- `WOODPECKER_EQUINIXMETAL_PLAN`
- exactly one of `WOODPECKER_EQUINIXMETAL_METRO` or `WOODPECKER_EQUINIXMETAL_FACILITY`

Equinix Metal support is currently experimental: it has not been tested by the project maintainers, as none of them have real provider access.

Useful optional settings:

- `WOODPECKER_EQUINIXMETAL_OPERATING_SYSTEM` (default: `ubuntu_24_04`)
- `WOODPECKER_EQUINIXMETAL_BILLING_CYCLE` (default: `hourly`)
- `WOODPECKER_EQUINIXMETAL_TAGS`
- `WOODPECKER_EQUINIXMETAL_PROJECT_SSH_KEYS`
- `WOODPECKER_EQUINIXMETAL_SPOT_INSTANCE`
- `WOODPECKER_EQUINIXMETAL_SPOT_PRICE_MAX`

## DigitalOcean

Set `WOODPECKER_PROVIDER=digitalocean` and configure at least:

- `WOODPECKER_DIGITALOCEAN_API_TOKEN` (or `WOODPECKER_DIGITALOCEAN_API_TOKEN_FILE`)

DigitalOcean support is currently experimental: it has not been tested by the project maintainers, as none of them have real provider access.

Useful optional settings:

- `WOODPECKER_DIGITALOCEAN_REGION` (default: `nyc1`)
- `WOODPECKER_DIGITALOCEAN_SIZE` (default: `s-1vcpu-1gb`)
- `WOODPECKER_DIGITALOCEAN_IMAGE` (default: `ubuntu-24-04-x64`, slug or name)
- `WOODPECKER_DIGITALOCEAN_SSH_KEYS` (names or fingerprints; if unset, a key named `random-autoscaler-key` is created and reused, its private key is discarded)
- `WOODPECKER_DIGITALOCEAN_TAGS`
- `WOODPECKER_DIGITALOCEAN_PUBLIC_IPV4_ENABLE` (default: `true`; set to `false` to create private droplets without a public network interface, which requires `WOODPECKER_DIGITALOCEAN_NAT_GATEWAY`)
- `WOODPECKER_DIGITALOCEAN_NAT_GATEWAY` (name or ID of an existing [VPC NAT gateway](https://docs.digitalocean.com/products/vpc-nat-gateway/); the agents are placed in the VPC it serves as default gateway so they can reach the server and pull images)
- `WOODPECKER_DIGITALOCEAN_PUBLIC_IPV6_ENABLE` (default: `true`, requires public IPv4)

## Azure

Set `WOODPECKER_PROVIDER=azure` and configure at least:

- `WOODPECKER_AZURE_SUBSCRIPTION_ID`
- `WOODPECKER_AZURE_RESOURCE_GROUP` (an existing resource group)
- `WOODPECKER_AZURE_LOCATION` (e.g. `westeurope`)
- `WOODPECKER_AZURE_SUBNET_ID` (resource ID of an existing subnet)

Azure support is currently experimental: it has not been tested by the project maintainers, as none of them have real provider access.

Authentication uses a service principal. Either pass it via `WOODPECKER_AZURE_TENANT_ID`, `WOODPECKER_AZURE_CLIENT_ID` and `WOODPECKER_AZURE_CLIENT_SECRET` (or `_FILE`), or rely on the default Azure credential chain (managed identity, `az login`, or the `AZURE_*` environment variables).

The autoscaler creates a public IP (optional), a network interface in the configured subnet and a VM per agent, all tagged with the autoscaler pool so only agents from the configured pool are listed or terminated.

Useful optional settings:

- `WOODPECKER_AZURE_VM_SIZE` (default: `Standard_B1s`)
- `WOODPECKER_AZURE_IMAGE` (default: `Canonical:ubuntu-24_04-lts:server:latest`, as `publisher:offer:sku:version`)
- `WOODPECKER_AZURE_OS_DISK_TYPE` (default: `Standard_LRS`)
- `WOODPECKER_AZURE_ADMIN_USERNAME` (default: `woodpecker`)
- `WOODPECKER_AZURE_SSH_PUBLIC_KEY` (or `_FILE`; if unset an ephemeral key is generated and its private key discarded)
- `WOODPECKER_AZURE_ASSIGN_PUBLIC_IP` (default: `true`)
- `WOODPECKER_AZURE_TAGS`

## OpenStack

Set `WOODPECKER_PROVIDER=openstack`. The prefix for all the following environment variables is `WOODPECKER_OPENSTACK_`.

You have to supply the `AUTH_URL` pointing to your Keystone. If necessary, you can also specifiy the `DOMAIN_NAME`, `REGION` and `PROJECT_NAME`.

Both `USERNAME`/`PASSWORD` authentication and application credentials via `APPLICATION_CREDENTIAL_ID` and `APPLICATION_CREDENTIAL_SECRET` are supported.
Credentials can also be read from files, to do so append `_FILE` to the appropriate variable name and set it to the file path.

You can select the flavor and image for the agent instances via `FLAVOR/IMAGE_NAME` or UUID reference (`FLAVOR/IMAGE_REF`).
If you set `VOLUME_SIZE`, block storage volumes are used.

You can add your OpenStack SSH keypair via `KEYPAIR`.

## Teardown policy

How idle agents are torn down depends on how the selected provider bills:

- **Per-second billing** (e.g. AWS, Scaleway): an idle agent is drained and removed once it has been idle for `WOODPECKER_AGENT_IDLE_TIMEOUT`. Holding an idle agent open buys nothing.
- **Hourly-rounded-up billing** (e.g. Linode, Hetzner Cloud, Vultr): a partial hour costs the same as a full one, so an idle agent is kept schedulable for the rest of the hour that has already been paid for and is only torn down just before its next hour boundary (anchored at its creation time). A busy agent simply rolls into the next paid hour; you never pay for an idle hour.

  The teardown window is `WOODPECKER_AGENT_BILLING_TEARDOWN_MARGIN` (default `2m`) plus `WOODPECKER_RECONCILIATION_INTERVAL`, so a reconciliation can never tick straight past the boundary. With the defaults (`2m` margin, `1m` interval) an idle agent becomes eligible for teardown in the last 3 minutes of each paid hour.

The billing model is selected automatically by the provider, so no extra configuration is required to benefit from this.

## Roadmap

- [ ] Add support for multiple providers
  - [x] Hetzner Cloud
  - [x] Amazon AWS
  - [ ] Google Cloud
  - [x] Azure **[experimental]** (untested by the maintainers against real provider access, see [above](#azure))
  - [x] Digital Ocean **[experimental]** (untested by the maintainers against real provider access, see [above](#digitalocean))
  - [x] Linode
  - [x] OpenStack **[experimental]**
  - [ ] Oracle Cloud
  - [x] Equinix Metal **[experimental]** (untested by the maintainers against real provider access, see [above](#equinix-metal))
  - [x] Vultr
  - [x] Scaleway
- [ ] Cleanup agents
  - [x] Remove agents which exist on the provider but are not in the server list (they wont be able to connect to the server anyway as their is no agent token for them)
  - [x] Remove agents from server list which do not exist on the provider
  - [ ] Remove agents which have not connected for a long time
- [x] Release as container image
- [x] Add docs
- [ ] Support agent deployment with specific attributes (e.g. platforms, architectures, etc.)
