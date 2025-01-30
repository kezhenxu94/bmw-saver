package schedule

import (
	"context"
	"time"
)

// Provider defines the interface for checking work hours
type Provider interface {
	// IsWorkTime checks if the given time is within working hours
	IsWorkTime(ctx context.Context, t time.Time) (bool, error)
}
