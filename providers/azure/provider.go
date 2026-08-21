package azure

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"

	"go.woodpecker-ci.org/autoscaler/config"
	"go.woodpecker-ci.org/autoscaler/engine/inits/cloudinit"
	"go.woodpecker-ci.org/autoscaler/engine/types"
	"go.woodpecker-ci.org/autoscaler/utils"
	"go.woodpecker-ci.org/woodpecker/v3/woodpecker-go/woodpecker"
)

// blackhole metadata services so running steps can not extract agent token from user-data
// https://learn.microsoft.com/en-us/azure/virtual-machines/instance-metadata-service (served over IPv4 169.254.169.254 only)
var blackholeMetadataAPI = []string{
	"ip -4 route add blackhole 169.254.169.254/32",
}

type provider struct {
	name           string
	config         *config.Config
	client         azureClient
	location       string
	subnetID       string
	vmSize         string
	osDiskType     string
	image          imageReference
	adminUsername  string
	sshPublicKey   string
	assignPublicIP bool
	tags           map[string]string
}

func New(_ context.Context, c *cli.Command, config *config.Config) (types.Provider, error) {
	p := &provider{
		name:           "azure",
		config:         config,
		location:       c.String("azure-location"),
		subnetID:       c.String("azure-subnet-id"),
		vmSize:         c.String("azure-vm-size"),
		osDiskType:     c.String("azure-os-disk-type"),
		adminUsername:  c.String("azure-admin-username"),
		assignPublicIP: c.Bool("azure-assign-public-ip"),
	}

	subscriptionID := c.String("azure-subscription-id")
	if err := p.validate(subscriptionID, c.String("azure-resource-group")); err != nil {
		return nil, fmt.Errorf("%s: %w", p.name, err)
	}

	image, err := parseImage(c.String("azure-image"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.name, err)
	}
	p.image = image

	sshPublicKey, generated, err := ensureSSHPublicKey(c.String("azure-ssh-public-key"))
	if err != nil {
		return nil, fmt.Errorf("%s: ssh key: %w", p.name, err)
	}
	if generated {
		log.Info().Msgf("%s: generated an ephemeral SSH key, its private key was discarded", p.name)
	}
	p.sshPublicKey = sshPublicKey

	tags, err := parseTags(c.StringSlice("azure-tags"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.name, err)
	}
	p.tags = utils.MergeMaps(map[string]string{
		tagPool:  config.PoolID,
		tagImage: c.String("azure-image"),
	}, tags)

	client, err := newAzureClient(c, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("%s: new client: %w", p.name, err)
	}
	p.client = client

	return p, nil
}

func (p *provider) validate(subscriptionID, resourceGroup string) error {
	switch {
	case subscriptionID == "":
		return ErrSubscriptionIDRequired
	case resourceGroup == "":
		return ErrResourceGroupRequired
	case p.location == "":
		return ErrLocationRequired
	case p.subnetID == "":
		return ErrSubnetIDRequired
	}

	return nil
}

func (p *provider) DeployAgent(ctx context.Context, agent *woodpecker.Agent) error {
	userData, err := cloudinit.RenderUserDataTemplate(p.config, agent, cloudinit.RenderOption{
		PreExec: blackholeMetadataAPI,
	})
	if err != nil {
		return fmt.Errorf("%s: cloudinit.RenderUserDataTemplate: %w", p.name, err)
	}

	var publicIPID string
	if p.assignPublicIP {
		publicIPID, err = p.client.CreatePublicIP(ctx, agent.Name+pipSuffix, p.publicIP())
		if err != nil {
			return fmt.Errorf("%s: CreatePublicIP: %w", p.name, err)
		}
	}

	nicID, err := p.client.CreateNIC(ctx, agent.Name+nicSuffix, p.networkInterface(publicIPID))
	if err != nil {
		// the public IP is already created; drop it so it does not leak
		p.rollback(ctx, agent.Name)
		return fmt.Errorf("%s: CreateNIC: %w", p.name, err)
	}

	vm := p.virtualMachine(agent.Name, nicID, base64.StdEncoding.EncodeToString([]byte(userData)))
	if err := p.client.CreateVM(ctx, agent.Name, vm); err != nil {
		// the NIC and public IP are already created; drop them so they do not leak
		p.rollback(ctx, agent.Name)
		return fmt.Errorf("%s: CreateVM: %w", p.name, err)
	}

	return nil
}

// rollback best-effort deletes the network resources created for an agent whose
// VM creation did not complete. Azure needs three sequential creations per
// agent, so without this a failed deploy would strand a NIC and a billed public
// IP that no later reconcile can reclaim (they are only reachable through their
// VM, which never came up). Deletes are idempotent, so a missing resource is
// fine; failures are logged, not returned, so the original error surfaces.
func (p *provider) rollback(ctx context.Context, name string) {
	if err := p.client.DeleteNIC(ctx, name+nicSuffix); err != nil {
		log.Warn().Err(err).Msgf("%s: rollback DeleteNIC for %s", p.name, name)
	}
	if p.assignPublicIP {
		if err := p.client.DeletePublicIP(ctx, name+pipSuffix); err != nil {
			log.Warn().Err(err).Msgf("%s: rollback DeletePublicIP for %s", p.name, name)
		}
	}
}

func (p *provider) RemoveAgent(ctx context.Context, agent *woodpecker.Agent) error {
	vm, err := p.getAgent(ctx, agent.Name)
	if err != nil {
		return fmt.Errorf("%s: getAgent: %w", p.name, err)
	}
	if vm == nil {
		return nil
	}

	// Delete in dependency order. The VM release frees the NIC (and OS disk via
	// their delete options); the explicit NIC and public IP deletes are
	// idempotent and clean up anything the cascade left behind.
	if err := p.client.DeleteVM(ctx, agent.Name); err != nil {
		return fmt.Errorf("%s: DeleteVM: %w", p.name, err)
	}
	if err := p.client.DeleteNIC(ctx, agent.Name+nicSuffix); err != nil {
		return fmt.Errorf("%s: DeleteNIC: %w", p.name, err)
	}
	if p.assignPublicIP {
		if err := p.client.DeletePublicIP(ctx, agent.Name+pipSuffix); err != nil {
			return fmt.Errorf("%s: DeletePublicIP: %w", p.name, err)
		}
	}

	return nil
}

func (p *provider) ListDeployedAgentNames(ctx context.Context) ([]string, error) {
	vms, err := p.client.ListVMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: ListVMs: %w", p.name, err)
	}

	names := make([]string, 0, len(vms))
	for _, vm := range vms {
		if !p.isPoolVM(vm) || vm.Name == nil {
			continue
		}
		if provisioningState(vm) == "Deleting" {
			continue
		}
		names = append(names, *vm.Name)
	}

	return names, nil
}

func (p *provider) BillingModel() types.BillingModel {
	return types.BillingPerSecond
}

func (p *provider) getAgent(ctx context.Context, name string) (*armcompute.VirtualMachine, error) {
	vms, err := p.client.ListVMs(ctx)
	if err != nil {
		return nil, err
	}

	for _, vm := range vms {
		if p.isPoolVM(vm) && vm.Name != nil && *vm.Name == name {
			return vm, nil
		}
	}

	return nil, nil
}

func (p *provider) isPoolVM(vm *armcompute.VirtualMachine) bool {
	value, ok := vm.Tags[tagPool]
	return ok && value != nil && *value == p.config.PoolID
}

func (p *provider) publicIP() armnetwork.PublicIPAddress {
	return armnetwork.PublicIPAddress{
		Location: to.Ptr(p.location),
		Tags:     azureTags(p.tags),
		SKU: &armnetwork.PublicIPAddressSKU{
			Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard),
		},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			PublicIPAddressVersion:   to.Ptr(armnetwork.IPVersionIPv4),
			DeleteOption:             to.Ptr(armnetwork.DeleteOptionsDelete),
		},
	}
}

func (p *provider) networkInterface(publicIPID string) armnetwork.Interface {
	ipConfig := &armnetwork.InterfaceIPConfiguration{
		Name: to.Ptr(primaryIPConfig),
		Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
			Primary:                   to.Ptr(true),
			PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
			Subnet:                    &armnetwork.Subnet{ID: to.Ptr(p.subnetID)},
		},
	}
	if publicIPID != "" {
		ipConfig.Properties.PublicIPAddress = &armnetwork.PublicIPAddress{ID: to.Ptr(publicIPID)}
	}

	return armnetwork.Interface{
		Location: to.Ptr(p.location),
		Tags:     azureTags(p.tags),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{ipConfig},
		},
	}
}

func (p *provider) virtualMachine(name, nicID, userData string) armcompute.VirtualMachine {
	return armcompute.VirtualMachine{
		Location: to.Ptr(p.location),
		Tags:     azureTags(p.tags),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(p.vmSize)),
			},
			StorageProfile: &armcompute.StorageProfile{
				ImageReference: &armcompute.ImageReference{
					Publisher: to.Ptr(p.image.publisher),
					Offer:     to.Ptr(p.image.offer),
					SKU:       to.Ptr(p.image.sku),
					Version:   to.Ptr(p.image.version),
				},
				OSDisk: &armcompute.OSDisk{
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					DeleteOption: to.Ptr(armcompute.DiskDeleteOptionTypesDelete),
					ManagedDisk: &armcompute.ManagedDiskParameters{
						StorageAccountType: to.Ptr(armcompute.StorageAccountTypes(p.osDiskType)),
					},
				},
			},
			OSProfile: &armcompute.OSProfile{
				ComputerName:  to.Ptr(name),
				AdminUsername: to.Ptr(p.adminUsername),
				CustomData:    to.Ptr(userData),
				LinuxConfiguration: &armcompute.LinuxConfiguration{
					DisablePasswordAuthentication: to.Ptr(true),
					SSH: &armcompute.SSHConfiguration{
						PublicKeys: []*armcompute.SSHPublicKey{{
							Path:    to.Ptr(fmt.Sprintf("/home/%s/.ssh/authorized_keys", p.adminUsername)),
							KeyData: to.Ptr(p.sshPublicKey),
						}},
					},
				},
			},
			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{{
					ID: to.Ptr(nicID),
					Properties: &armcompute.NetworkInterfaceReferenceProperties{
						Primary:      to.Ptr(true),
						DeleteOption: to.Ptr(armcompute.DeleteOptionsDelete),
					},
				}},
			},
		},
	}
}

func provisioningState(vm *armcompute.VirtualMachine) string {
	if vm.Properties == nil || vm.Properties.ProvisioningState == nil {
		return ""
	}
	return *vm.Properties.ProvisioningState
}
