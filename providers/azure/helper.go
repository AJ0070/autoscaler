package azure

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/ssh"

	"go.woodpecker-ci.org/autoscaler/engine"
	"go.woodpecker-ci.org/autoscaler/utils"
)

// Azure tag names may not contain the characters < > % & \ ? / and periods are
// discouraged, so the engine labels (wp.autoscaler/...) are mapped to
// wp-autoscaler-... on this provider.
var (
	tagPrefix = azureTagKey(engine.LabelPrefix)
	tagPool   = azureTagKey(engine.LabelPool)
	tagImage  = azureTagKey(engine.LabelImage)
)

func azureTagKey(s string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(s)
}

// newCredential prefers service principal credentials passed via flags and
// falls back to the default Azure credential chain (managed identity, az login,
// environment) otherwise.
func newCredential(c *cli.Command) (azcore.TokenCredential, error) {
	tenantID := c.String("azure-tenant-id")
	clientID := c.String("azure-client-id")
	clientSecret := c.String("azure-client-secret")

	if tenantID != "" || clientID != "" || clientSecret != "" {
		if tenantID == "" || clientID == "" || clientSecret == "" {
			return nil, ErrIncompleteCredentials
		}
		return azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	}

	return azidentity.NewDefaultAzureCredential(nil)
}

// armClient is the real azureClient, driving the SDK long-running-operation
// pollers against a single resource group.
type armClient struct {
	resourceGroup string
	vms           *armcompute.VirtualMachinesClient
	nics          *armnetwork.InterfacesClient
	pips          *armnetwork.PublicIPAddressesClient
}

func newAzureClient(c *cli.Command, subscriptionID string) (azureClient, error) {
	cred, err := newCredential(c)
	if err != nil {
		return nil, err
	}

	vms, err := armcompute.NewVirtualMachinesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	nics, err := armnetwork.NewInterfacesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}
	pips, err := armnetwork.NewPublicIPAddressesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}

	return &armClient{
		resourceGroup: c.String("azure-resource-group"),
		vms:           vms,
		nics:          nics,
		pips:          pips,
	}, nil
}

func (a *armClient) CreatePublicIP(ctx context.Context, name string, pip armnetwork.PublicIPAddress) (string, error) {
	poller, err := a.pips.BeginCreateOrUpdate(ctx, a.resourceGroup, name, pip, nil)
	if err != nil {
		return "", err
	}
	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", err
	}
	if resp.ID == nil {
		return "", fmt.Errorf("public IP %s has no ID", name)
	}
	return *resp.ID, nil
}

func (a *armClient) CreateNIC(ctx context.Context, name string, nic armnetwork.Interface) (string, error) {
	poller, err := a.nics.BeginCreateOrUpdate(ctx, a.resourceGroup, name, nic, nil)
	if err != nil {
		return "", err
	}
	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", err
	}
	if resp.ID == nil {
		return "", fmt.Errorf("network interface %s has no ID", name)
	}
	return *resp.ID, nil
}

func (a *armClient) CreateVM(ctx context.Context, name string, vm armcompute.VirtualMachine) error {
	poller, err := a.vms.BeginCreateOrUpdate(ctx, a.resourceGroup, name, vm, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (a *armClient) DeleteVM(ctx context.Context, name string) error {
	poller, err := a.vms.BeginDelete(ctx, a.resourceGroup, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (a *armClient) DeleteNIC(ctx context.Context, name string) error {
	poller, err := a.nics.BeginDelete(ctx, a.resourceGroup, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (a *armClient) DeletePublicIP(ctx context.Context, name string) error {
	poller, err := a.pips.BeginDelete(ctx, a.resourceGroup, name, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (a *armClient) ListVMs(ctx context.Context) ([]*armcompute.VirtualMachine, error) {
	var all []*armcompute.VirtualMachine
	pager := a.vms.NewListPager(a.resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Value...)
	}
	return all, nil
}

// parseImage parses a publisher:offer:sku:version platform image URN.
func parseImage(urn string) (imageReference, error) {
	parts := strings.Split(urn, ":")
	if len(parts) != imageURNParts {
		return imageReference{}, fmt.Errorf("%w: %q", ErrImageInvalid, urn)
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return imageReference{}, fmt.Errorf("%w: %q", ErrImageInvalid, urn)
		}
	}
	return imageReference{publisher: parts[0], offer: parts[1], sku: parts[2], version: parts[3]}, nil
}

// parseTags parses key=value pairs and rejects keys Azure does not accept or
// that collide with the tags managed by the autoscaler.
func parseTags(raw []string) (map[string]string, error) {
	tags, err := utils.SliceToMap(raw, "=")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTag, err)
	}

	for key := range tags {
		if strings.ContainsAny(key, `<>%&\?/`) {
			return nil, fmt.Errorf("%w: key %q contains a character Azure does not allow in tag names", ErrInvalidTag, key)
		}
		if strings.HasPrefix(key, tagPrefix) {
			return nil, fmt.Errorf("%w: %s", ErrReservedTagPrefix, tagPrefix)
		}
	}

	return tags, nil
}

func azureTags(tags map[string]string) map[string]*string {
	out := make(map[string]*string, len(tags))
	for key, value := range tags {
		out[key] = to.Ptr(value)
	}
	return out
}

// ensureSSHPublicKey returns the configured public key, or generates an
// ephemeral one whose private key is discarded when none is configured. SSH
// access is not required for the agent to work, but Azure needs a key when
// password authentication is disabled.
func ensureSSHPublicKey(configured string) (key string, generated bool, err error) {
	if strings.TrimSpace(configured) != "" {
		return configured, false, nil
	}

	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", false, err
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return "", false, err
	}
	authorized := string(ssh.MarshalAuthorizedKey(sshPublicKey))
	return strings.TrimSpace(authorized) + " " + autoSSHKeyComment, true, nil
}
