package schedule

import (
	"context"
	"log/slog"
	"time"
)

// CompositeProvider combines multiple schedule providers.
// It considers it work time only if ALL providers agree it's work time.
type CompositeProvider struct {
	providers []Provider
}

// NewCompositeProvider creates a new composite provider with the given providers
func NewCompositeProvider(providers ...Provider) *CompositeProvider {
	return &CompositeProvider{
		providers: providers,
	}
}

// IsWorkTime returns true only if all providers agree it's work time
func (p *CompositeProvider) IsWorkTime(ctx context.Context, t time.Time) (bool, error) {
	for _, provider := range p.providers {
		isWork, err := provider.IsWorkTime(ctx, t)
		if err != nil {
			return false, err
		}
		slog.Debug("IsWorkTime", "provider", provider, "isWork", isWork)
		// If any provider says it's not work time, return false
		if !isWork {
			return false, nil
		}
	}
	// All providers agree it's work time
	return true, nil
}
