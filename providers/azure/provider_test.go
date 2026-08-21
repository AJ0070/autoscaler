package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"go.woodpecker-ci.org/autoscaler/config"
	"go.woodpecker-ci.org/woodpecker/v3/woodpecker-go/woodpecker"
)

type createdVM struct {
	name string
	vm   armcompute.VirtualMachine
}

type fakeAzureClient struct {
	vms []*armcompute.VirtualMachine

	createdVM  *createdVM
	createdNIC *armnetwork.Interface
	createdPIP *armnetwork.PublicIPAddress
	deleted    []string

	failCreateVM  error
	failCreateNIC error
	failCreatePIP error
}

func (f *fakeAzureClient) CreatePublicIP(_ context.Context, name string, pip armnetwork.PublicIPAddress) (string, error) {
	if f.failCreatePIP != nil {
		return "", f.failCreatePIP
	}
	f.createdPIP = &pip
	return "/subscriptions/s/pip/" + name, nil
}

func (f *fakeAzureClient) CreateNIC(_ context.Context, name string, nic armnetwork.Interface) (string, error) {
	if f.failCreateNIC != nil {
		return "", f.failCreateNIC
	}
	f.createdNIC = &nic
	return "/subscriptions/s/nic/" + name, nil
}

func (f *fakeAzureClient) CreateVM(_ context.Context, name string, vm armcompute.VirtualMachine) error {
	if f.failCreateVM != nil {
		return f.failCreateVM
	}
	f.createdVM = &createdVM{name: name, vm: vm}
	return nil
}

func (f *fakeAzureClient) DeleteVM(_ context.Context, name string) error {
	f.deleted = append(f.deleted, "vm:"+name)
	return nil
}

func (f *fakeAzureClient) DeleteNIC(_ context.Context, name string) error {
	f.deleted = append(f.deleted, "nic:"+name)
	return nil
}

func (f *fakeAzureClient) DeletePublicIP(_ context.Context, name string) error {
	f.deleted = append(f.deleted, "pip:"+name)
	return nil
}

func (f *fakeAzureClient) ListVMs(context.Context) ([]*armcompute.VirtualMachine, error) {
	return f.vms, nil
}

func poolVM(name, pool, state string) *armcompute.VirtualMachine {
	vm := &armcompute.VirtualMachine{
		Name: to.Ptr(name),
		Tags: map[string]*string{tagPool: to.Ptr(pool)},
	}
	if state != "" {
		vm.Properties = &armcompute.VirtualMachineProperties{ProvisioningState: to.Ptr(state)}
	}
	return vm
}

func newTestProvider(client azureClient) *provider {
	return &provider{
		name:           "azure",
		config:         &config.Config{PoolID: "pool-1", GRPCAddress: "grpc.example.com", Image: "woodpeckerci/woodpecker-agent:next"},
		client:         client,
		location:       "westeurope",
		subnetID:       "/subscriptions/s/subnets/agents",
		vmSize:         defaultVMSize,
		osDiskType:     defaultOSDiskType,
		image:          imageReference{publisher: "Canonical", offer: "ubuntu-24_04-lts", sku: "server", version: "latest"},
		adminUsername:  defaultAdminUser,
		sshPublicKey:   "ssh-ed25519 AAAA test",
		assignPublicIP: true,
		tags:           map[string]string{tagPool: "pool-1", tagImage: "img", "team": "ci"},
	}
}

func newTestCommand(t *testing.T, args []string) *cli.Command {
	t.Helper()

	var captured *cli.Command
	cmd := &cli.Command{
		Flags: ProviderFlags,
		Action: func(_ context.Context, c *cli.Command) error {
			captured = c
			return nil
		},
	}

	require.NoError(t, cmd.Run(t.Context(), append([]string{"test"}, args...)))
	require.NotNil(t, captured)

	return captured
}

func requiredArgs() []string {
	return []string{
		"--azure-subscription-id=sub-1",
		"--azure-resource-group=rg-1",
		"--azure-location=westeurope",
		"--azure-subnet-id=/subscriptions/s/subnets/agents",
		"--azure-ssh-public-key=ssh-ed25519 AAAA test",
	}
}

func TestNewValidatesConfig(t *testing.T) {
	base := requiredArgs()

	tests := []struct {
		name    string
		args    []string
		wantErr error
	}{
		{name: "missing subscription", args: base[1:], wantErr: ErrSubscriptionIDRequired},
		{name: "missing resource group", args: append([]string{base[0]}, base[2:]...), wantErr: ErrResourceGroupRequired},
		{name: "missing location", args: append([]string{base[0], base[1]}, base[3:]...), wantErr: ErrLocationRequired},
		{name: "missing subnet", args: append([]string{base[0], base[1], base[2]}, base[4:]...), wantErr: ErrSubnetIDRequired},
		{name: "invalid image", args: append(base, "--azure-image=not-a-urn"), wantErr: ErrImageInvalid},
		{name: "reserved tag", args: append(base, "--azure-tags="+tagPrefix+"x=y"), wantErr: ErrReservedTagPrefix},
		{name: "invalid tag key", args: append(base, "--azure-tags=a/b=c"), wantErr: ErrInvalidTag},
		{name: "incomplete credentials", args: append(base, "--azure-tenant-id=t"), wantErr: ErrIncompleteCredentials},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(t.Context(), newTestCommand(t, tt.args), &config.Config{PoolID: "pool-1"})
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNewResolvesTagsAndImage(t *testing.T) {
	cmd := newTestCommand(t, append(requiredArgs(), "--azure-tags=team=ci"))

	p, err := New(t.Context(), cmd, &config.Config{PoolID: "pool-1"})
	require.NoError(t, err)

	azp, ok := p.(*provider)
	require.True(t, ok)
	assert.Equal(t, "Canonical", azp.image.publisher)
	assert.Equal(t, "latest", azp.image.version)
	assert.Equal(t, "pool-1", azp.tags[tagPool])
	assert.Equal(t, "ci", azp.tags["team"])
	assert.Contains(t, azp.tags, tagImage)
}

func TestNewGeneratesSSHKeyWhenNoneConfigured(t *testing.T) {
	// the four required flags without an ssh key, so an ephemeral one is generated
	cmd := newTestCommand(t, requiredArgs()[:4])

	p, err := New(t.Context(), cmd, &config.Config{PoolID: "pool-1"})
	require.NoError(t, err)

	azp, ok := p.(*provider)
	require.True(t, ok)
	assert.Contains(t, azp.sshPublicKey, "ssh-ed25519 ")
	assert.Contains(t, azp.sshPublicKey, autoSSHKeyComment)
}

func TestParseImage(t *testing.T) {
	img, err := parseImage("Canonical:ubuntu-24_04-lts:server:latest")
	require.NoError(t, err)
	assert.Equal(t, imageReference{"Canonical", "ubuntu-24_04-lts", "server", "latest"}, img)

	for _, bad := range []string{"a:b:c", "a:b:c:", ":b:c:d", "a:b:c:d:e"} {
		_, err := parseImage(bad)
		require.ErrorIs(t, err, ErrImageInvalid, bad)
	}
}

func TestDeployAgentCreatesResources(t *testing.T) {
	client := &fakeAzureClient{}
	p := newTestProvider(client)

	require.NoError(t, p.DeployAgent(t.Context(), &woodpecker.Agent{Name: "pool-1-agent-1", Token: "secret"}))

	// public IP
	require.NotNil(t, client.createdPIP)
	assert.Equal(t, armnetwork.PublicIPAddressSKUNameStandard, *client.createdPIP.SKU.Name)

	// NIC wired to the subnet and the public IP
	require.NotNil(t, client.createdNIC)
	ipConfig := client.createdNIC.Properties.IPConfigurations[0]
	assert.Equal(t, "/subscriptions/s/subnets/agents", *ipConfig.Properties.Subnet.ID)
	require.NotNil(t, ipConfig.Properties.PublicIPAddress)
	assert.Equal(t, "/subscriptions/s/pip/pool-1-agent-1-pip", *ipConfig.Properties.PublicIPAddress.ID)

	// VM
	require.NotNil(t, client.createdVM)
	assert.Equal(t, "pool-1-agent-1", client.createdVM.name)
	vm := client.createdVM.vm
	assert.Equal(t, "westeurope", *vm.Location)
	assert.Equal(t, armcompute.VirtualMachineSizeTypes(defaultVMSize), *vm.Properties.HardwareProfile.VMSize)
	assert.Equal(t, "Canonical", *vm.Properties.StorageProfile.ImageReference.Publisher)
	assert.Equal(t, armcompute.DiskDeleteOptionTypesDelete, *vm.Properties.StorageProfile.OSDisk.DeleteOption)
	assert.Equal(t, "/subscriptions/s/nic/pool-1-agent-1-nic", *vm.Properties.NetworkProfile.NetworkInterfaces[0].ID)
	assert.Equal(t, armcompute.DeleteOptionsDelete, *vm.Properties.NetworkProfile.NetworkInterfaces[0].Properties.DeleteOption)
	assert.True(t, *vm.Properties.OSProfile.LinuxConfiguration.DisablePasswordAuthentication)
	assert.Equal(t, "pool-1-agent-1", *vm.Properties.OSProfile.ComputerName)
	assert.Equal(t, defaultAdminUser, *vm.Properties.OSProfile.AdminUsername)
	assert.Equal(t, "ssh-ed25519 AAAA test", *vm.Properties.OSProfile.LinuxConfiguration.SSH.PublicKeys[0].KeyData)
	assert.Equal(t, armcompute.StorageAccountTypes(defaultOSDiskType), *vm.Properties.StorageProfile.OSDisk.ManagedDisk.StorageAccountType)
	assert.Equal(t, "pool-1", *vm.Tags[tagPool])

	userData, err := base64.StdEncoding.DecodeString(*vm.Properties.OSProfile.CustomData)
	require.NoError(t, err)
	assert.Contains(t, string(userData), "#cloud-config")
	assert.Contains(t, string(userData), "grpc.example.com")
	assert.Contains(t, string(userData), "secret")
	assert.Contains(t, string(userData), "ip -4 route add blackhole 169.254.169.254/32")
}

func TestDeployAgentWithoutPublicIP(t *testing.T) {
	client := &fakeAzureClient{}
	p := newTestProvider(client)
	p.assignPublicIP = false

	require.NoError(t, p.DeployAgent(t.Context(), &woodpecker.Agent{Name: "pool-1-agent-1"}))

	assert.Nil(t, client.createdPIP)
	assert.Nil(t, client.createdNIC.Properties.IPConfigurations[0].Properties.PublicIPAddress)
}

func TestDeployAgentCreateVMError(t *testing.T) {
	client := &fakeAzureClient{failCreateVM: errors.New("boom")}
	p := newTestProvider(client)

	err := p.DeployAgent(t.Context(), &woodpecker.Agent{Name: "pool-1-agent-1"})
	require.ErrorContains(t, err, "CreateVM: boom")
	// the NIC and public IP created before the VM must be rolled back
	assert.Equal(t, []string{"nic:pool-1-agent-1-nic", "pip:pool-1-agent-1-pip"}, client.deleted)
}

func TestDeployAgentRollsBackPublicIPOnNICError(t *testing.T) {
	client := &fakeAzureClient{failCreateNIC: errors.New("boom")}
	p := newTestProvider(client)

	err := p.DeployAgent(t.Context(), &woodpecker.Agent{Name: "pool-1-agent-1"})
	require.ErrorContains(t, err, "CreateNIC: boom")
	// the missing NIC delete is a no-op; the public IP must still be dropped
	assert.Equal(t, []string{"nic:pool-1-agent-1-nic", "pip:pool-1-agent-1-pip"}, client.deleted)
}

func TestDeployAgentNoRollbackOfPublicIPWhenDisabled(t *testing.T) {
	client := &fakeAzureClient{failCreateVM: errors.New("boom")}
	p := newTestProvider(client)
	p.assignPublicIP = false

	err := p.DeployAgent(t.Context(), &woodpecker.Agent{Name: "pool-1-agent-1"})
	require.ErrorContains(t, err, "CreateVM: boom")
	assert.Equal(t, []string{"nic:pool-1-agent-1-nic"}, client.deleted)
}

func TestListDeployedAgentNamesFiltersPoolAndState(t *testing.T) {
	client := &fakeAzureClient{vms: []*armcompute.VirtualMachine{
		poolVM("pool-1-agent-1", "pool-1", "Succeeded"),
		poolVM("pool-2-agent-1", "pool-2", "Succeeded"),
		poolVM("pool-1-agent-2", "pool-1", "Deleting"),
		poolVM("pool-1-agent-3", "pool-1", "Creating"),
		{Name: to.Ptr("untagged")},
		{Name: to.Ptr("nil-tag-value"), Tags: map[string]*string{tagPool: nil}},
	}}
	p := newTestProvider(client)

	names, err := p.ListDeployedAgentNames(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"pool-1-agent-1", "pool-1-agent-3"}, names)
}

func TestRemoveAgentDeletesResourcesInOrder(t *testing.T) {
	client := &fakeAzureClient{vms: []*armcompute.VirtualMachine{
		poolVM("pool-1-agent-1", "pool-1", "Succeeded"),
	}}
	p := newTestProvider(client)

	require.NoError(t, p.RemoveAgent(t.Context(), &woodpecker.Agent{Name: "pool-1-agent-1"}))
	assert.Equal(t, []string{"vm:pool-1-agent-1", "nic:pool-1-agent-1-nic", "pip:pool-1-agent-1-pip"}, client.deleted)
}

func TestRemoveAgentWithoutPublicIP(t *testing.T) {
	client := &fakeAzureClient{vms: []*armcompute.VirtualMachine{
		poolVM("pool-1-agent-1", "pool-1", "Succeeded"),
	}}
	p := newTestProvider(client)
	p.assignPublicIP = false

	require.NoError(t, p.RemoveAgent(t.Context(), &woodpecker.Agent{Name: "pool-1-agent-1"}))
	assert.Equal(t, []string{"vm:pool-1-agent-1", "nic:pool-1-agent-1-nic"}, client.deleted)
}

func TestRemoveAgentIgnoresUnknownAndOtherPools(t *testing.T) {
	client := &fakeAzureClient{vms: []*armcompute.VirtualMachine{
		poolVM("pool-2-agent-1", "pool-2", "Succeeded"),
	}}
	p := newTestProvider(client)

	require.NoError(t, p.RemoveAgent(t.Context(), &woodpecker.Agent{Name: "pool-2-agent-1"}))
	assert.Empty(t, client.deleted)
}
