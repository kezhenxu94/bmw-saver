package providers

import (
	"context"
	"fmt"
)

// ErrNoSavedState indicates that there is no saved state to restore for a node pool
type ErrNoSavedState struct {
	NodePool string
}

func (e *ErrNoSavedState) Error() string {
	return fmt.Sprintf("no saved state found for node pool: %s", e.NodePool)
}

// IsNoSavedStateError checks if an error is a NoSavedState error
func IsNoSavedStateError(err error) bool {
	_, ok := err.(*ErrNoSavedState)
	return ok
}

// CloudProvider defines the interface for cloud-specific node pool scaling
type CloudProvider interface {
	// ScaleNodePool scales a node pool to the specified count.
	// It returns an error if the scaling operation fails.
	ScaleNodePool(ctx context.Context, nodePoolName string, count int32) error

	// RestoreNodePool restores a node pool to its saved configuration.
	// It returns an error if the restore operation fails.
	RestoreNodePool(ctx context.Context, nodePoolName string) error
}

// NewCloudProvider creates a new cloud provider based on the provider type.
// It returns an error if the provider type is not supported.
func NewCloudProvider(providerType string) (CloudProvider, error) {
	switch providerType {
	case "gke":
		return NewGKEProvider()
	case "aws":
		return NewAWSProvider()
	case "azure":
		return NewAzureProvider()
	default:
		return nil, fmt.Errorf("unsupported cloud provider: %s", providerType)
	}
}
