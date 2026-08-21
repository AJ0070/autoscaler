package azure

import (
	"os"

	"github.com/urfave/cli/v3"
)

const category = "Azure"

var ProviderFlags = []cli.Flag{
	// Service principal authentication (used when set, otherwise the default
	// Azure credential chain, e.g. managed identity or `az login`, is used).
	&cli.StringFlag{
		Name:     "azure-tenant-id",
		Usage:    "Azure AD tenant ID (service principal authentication)",
		Sources:  cli.EnvVars("WOODPECKER_AZURE_TENANT_ID"),
		Category: category,
	},
	&cli.StringFlag{
		Name:     "azure-client-id",
		Usage:    "Azure AD application (client) ID (service principal authentication)",
		Sources:  cli.EnvVars("WOODPECKER_AZURE_CLIENT_ID"),
		Category: category,
	},
	&cli.StringFlag{
		Name:  "azure-client-secret",
		Usage: "Azure AD application client secret (service principal authentication)",
		Sources: cli.NewValueSourceChain(
			cli.EnvVar("WOODPECKER_AZURE_CLIENT_SECRET"),
			cli.File(os.Getenv("WOODPECKER_AZURE_CLIENT_SECRET_FILE")),
		),
		Category: category,
	},
	// Placement.
	&cli.StringFlag{
		Name:     "azure-subscription-id",
		Usage:    "Azure subscription ID the agents are created in",
		Sources:  cli.EnvVars("WOODPECKER_AZURE_SUBSCRIPTION_ID"),
		Category: category,
	},
	&cli.StringFlag{
		Name:     "azure-resource-group",
		Usage:    "existing Azure resource group the agents are created in",
		Sources:  cli.EnvVars("WOODPECKER_AZURE_RESOURCE_GROUP"),
		Category: category,
	},
	&cli.StringFlag{
		Name:     "azure-location",
		Usage:    "Azure region the agents are created in, e.g. westeurope",
		Sources:  cli.EnvVars("WOODPECKER_AZURE_LOCATION"),
		Category: category,
	},
	&cli.StringFlag{
		Name:     "azure-subnet-id",
		Usage:    "resource ID of an existing subnet the agents are attached to",
		Sources:  cli.EnvVars("WOODPECKER_AZURE_SUBNET_ID"),
		Category: category,
	},
	// Instance shape.
	&cli.StringFlag{
		Name:     "azure-vm-size",
		Usage:    "Azure VM size",
		Value:    defaultVMSize,
		Sources:  cli.EnvVars("WOODPECKER_AZURE_VM_SIZE"),
		Category: category,
	},
	&cli.StringFlag{
		Name:     "azure-image",
		Usage:    "platform image URN as publisher:offer:sku:version",
		Value:    defaultImage,
		Sources:  cli.EnvVars("WOODPECKER_AZURE_IMAGE"),
		Category: category,
	},
	&cli.StringFlag{
		Name:     "azure-os-disk-type",
		Usage:    "managed OS disk storage account type",
		Value:    defaultOSDiskType,
		Sources:  cli.EnvVars("WOODPECKER_AZURE_OS_DISK_TYPE"),
		Category: category,
	},
	// Access.
	&cli.StringFlag{
		Name:     "azure-admin-username",
		Usage:    "admin username created on the agents",
		Value:    defaultAdminUser,
		Sources:  cli.EnvVars("WOODPECKER_AZURE_ADMIN_USERNAME"),
		Category: category,
	},
	&cli.StringFlag{
		Name:  "azure-ssh-public-key",
		Usage: "SSH public key installed for the admin user; if unset an ephemeral key is generated and its private key discarded",
		Sources: cli.NewValueSourceChain(
			cli.EnvVar("WOODPECKER_AZURE_SSH_PUBLIC_KEY"),
			cli.File(os.Getenv("WOODPECKER_AZURE_SSH_PUBLIC_KEY_FILE")),
		),
		Category: category,
	},
	&cli.BoolFlag{
		Name:     "azure-assign-public-ip",
		Usage:    "assign a public IPv4 address to the agents",
		Value:    true,
		Sources:  cli.EnvVars("WOODPECKER_AZURE_ASSIGN_PUBLIC_IP"),
		Category: category,
	},
	&cli.StringSliceFlag{
		Name:     "azure-tags",
		Usage:    "additional tags for the agents as key=value pairs",
		Sources:  cli.EnvVars("WOODPECKER_AZURE_TAGS"),
		Category: category,
	},
}
