package azure

import (
	"context"
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

var (
	ErrSubscriptionIDRequired = errors.New("subscription ID is required")
	ErrResourceGroupRequired  = errors.New("resource group is required")
	ErrLocationRequired       = errors.New("location is required")
	ErrSubnetIDRequired       = errors.New("subnet ID is required")
	ErrIncompleteCredentials  = errors.New("tenant ID, client ID and client secret must all be set for service principal authentication")
	ErrImageInvalid           = errors.New("image must be publisher:offer:sku:version")
	ErrInvalidTag             = errors.New("invalid tag")
	ErrReservedTagPrefix      = errors.New("illegal tag prefix")
)

const (
	defaultVMSize     = "Standard_B1s"
	defaultImage      = "Canonical:ubuntu-24_04-lts:server:latest"
	defaultAdminUser  = "woodpecker"
	defaultOSDiskType = "Standard_LRS"
	autoSSHKeyComment = "woodpecker-autoscaler"
	nicSuffix         = "-nic"
	pipSuffix         = "-pip"
	primaryIPConfig   = "ipconfig1"

	imageURNParts = 4
)

// azureClient is the subset of the Azure compute and network APIs the provider
// needs. The real implementation drives the SDK's long-running-operation
// pollers; tests provide a fake.
type azureClient interface {
	CreatePublicIP(ctx context.Context, name string, pip armnetwork.PublicIPAddress) (id string, err error)
	CreateNIC(ctx context.Context, name string, nic armnetwork.Interface) (id string, err error)
	CreateVM(ctx context.Context, name string, vm armcompute.VirtualMachine) error
	DeleteVM(ctx context.Context, name string) error
	DeleteNIC(ctx context.Context, name string) error
	DeletePublicIP(ctx context.Context, name string) error
	ListVMs(ctx context.Context) ([]*armcompute.VirtualMachine, error)
}

// imageReference is a parsed publisher:offer:sku:version platform image URN.
type imageReference struct {
	publisher string
	offer     string
	sku       string
	version   string
}
