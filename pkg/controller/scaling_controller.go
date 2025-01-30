package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kezhenxu94/bmw-saver/pkg/config"
	"github.com/kezhenxu94/bmw-saver/pkg/providers"
	"github.com/kezhenxu94/bmw-saver/pkg/schedule"

	"log/slog"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// initOptions contains options for initializing providers
type initOptions struct {
	// If true, log errors instead of returning them
	logErrors bool
}

// ScalingController manages node pool scaling based on work hours.
type ScalingController struct {
	client    *kubernetes.Clientset
	config    config.Config
	providers map[string]providers.CloudProvider
	scheduler schedule.Provider
	mu        sync.RWMutex
}

// NewScalingController creates a new scaling controller with the provided configuration.
// It initializes cloud providers for each node pool specification.
func NewScalingController(client *kubernetes.Clientset, cfg config.Config) (*ScalingController, error) {
	sc := &ScalingController{
		client:    client,
		config:    cfg,
		providers: make(map[string]providers.CloudProvider),
	}

	if err := sc.initScheduleProviders(cfg, initOptions{logErrors: false}); err != nil {
		return nil, err
	}

	if err := sc.initCloudProviders(cfg, initOptions{logErrors: false}); err != nil {
		return nil, err
	}

	return sc, nil
}

// initScheduleProviders initializes all schedule providers based on configuration
func (sc *ScalingController) initScheduleProviders(cfg config.Config, opts initOptions) error {
	var scheduleProviders []schedule.Provider

	// Always add static provider if configured
	if cfg.Schedule.StartTime != "" && cfg.Schedule.EndTime != "" && cfg.Schedule.TimeZone != "" {
		workDays := sc.getWorkDays(cfg.Schedule.WorkDays)
		scheduleProviders = append(scheduleProviders, schedule.NewStaticProvider(
			cfg.Schedule.StartTime,
			cfg.Schedule.EndTime,
			cfg.Schedule.TimeZone,
			workDays,
		))
	}

	// Add Google Calendar provider if configured
	if cfg.Schedule.GoogleCalendar != nil {
		slog.Info("Using Google Calendar provider")

		syncInterval, err := sc.getSyncInterval(cfg.Schedule.GoogleCalendar.SyncInterval)
		if err != nil {
			if opts.logErrors {
				slog.Error("Invalid sync interval, using default", "error", err)
			} else {
				return fmt.Errorf("invalid sync interval: %v", err)
			}
		}

		cacheDays := sc.getCacheDays(cfg.Schedule.GoogleCalendar.CacheDays)

		gcalProvider, err := schedule.NewGoogleCalendarProvider(
			cfg.Schedule.GoogleCalendar.CredentialsPath,
			cfg.Schedule.GoogleCalendar.CalendarID,
			cfg.Schedule.GoogleCalendar.OffTimeEvents,
			syncInterval,
			cacheDays,
		)
		if err != nil {
			if opts.logErrors {
				slog.Error("Failed to create Google Calendar provider", "error", err)
			} else {
				return fmt.Errorf("failed to create Google Calendar provider: %v", err)
			}
		} else {
			scheduleProviders = append(scheduleProviders, gcalProvider)
		}
	}

	if cfg.Schedule.ICSCalendar != nil {
		syncInterval := 1 * time.Hour
		if cfg.Schedule.ICSCalendar.SyncInterval != "" {
			d, err := time.ParseDuration(cfg.Schedule.ICSCalendar.SyncInterval)
			if err != nil {
				return fmt.Errorf("invalid sync interval: %v", err)
			}
			syncInterval = d
		}

		icsProvider, err := schedule.NewICSCalendarProvider(
			cfg.Schedule.ICSCalendar.URL,
			syncInterval,
			cfg.Schedule.ICSCalendar.WorkDayPatterns,
			cfg.Schedule.ICSCalendar.HolidayPatterns,
		)
		if err != nil {
			return fmt.Errorf("failed to create ICS Calendar provider: %v", err)
		}
		scheduleProviders = append(scheduleProviders, icsProvider)
	}

	if len(scheduleProviders) == 0 {
		if opts.logErrors {
			slog.Error("No schedule providers configured")
			return nil
		}
		return fmt.Errorf("no schedule providers configured")
	}

	// Create composite provider from all configured providers
	sc.scheduler = schedule.NewCompositeProvider(scheduleProviders...)
	return nil
}

// initCloudProviders initializes cloud providers for each node pool
func (sc *ScalingController) initCloudProviders(cfg config.Config, opts initOptions) error {
	// Clear existing providers
	sc.providers = make(map[string]providers.CloudProvider)

	// Initialize cloud providers
	for _, spec := range cfg.NodeSpecs {
		provider, err := providers.NewCloudProvider(spec.CloudProvider)
		if err != nil {
			if opts.logErrors {
				slog.Error("Failed to create provider for node pool",
					"node_pool", spec.NodePoolName,
					"error", err,
				)
				continue
			}
			return fmt.Errorf("failed to create provider for node pool %s: %v", spec.NodePoolName, err)
		}
		sc.providers[spec.NodePoolName] = provider
	}
	return nil
}

// getWorkDays converts WorkDays config to a map
func (sc *ScalingController) getWorkDays(workDays *config.WorkDays) map[time.Weekday]bool {
	if workDays == nil {
		return map[time.Weekday]bool{
			time.Monday:    true,
			time.Tuesday:   true,
			time.Wednesday: true,
			time.Thursday:  true,
			time.Friday:    true,
		}
	}
	return map[time.Weekday]bool{
		time.Monday:    workDays.Monday,
		time.Tuesday:   workDays.Tuesday,
		time.Wednesday: workDays.Wednesday,
		time.Thursday:  workDays.Thursday,
		time.Friday:    workDays.Friday,
		time.Saturday:  workDays.Saturday,
		time.Sunday:    workDays.Sunday,
	}
}

// getSyncInterval parses and validates the sync interval
func (sc *ScalingController) getSyncInterval(interval string) (time.Duration, error) {
	if interval == "" {
		return time.Hour, nil
	}
	return time.ParseDuration(interval)
}

// getCacheDays returns the number of days to cache
func (sc *ScalingController) getCacheDays(days int) int {
	if days <= 0 {
		return 7
	}
	return days
}

// Run starts the controller's reconciliation loop.
// It runs indefinitely until an error occurs.
func (sc *ScalingController) Run() error {
	slog.Info("Starting scaling controller")
	wait.Forever(sc.reconcile, time.Minute)
	return nil
}

// UpdateConfig updates the controller's configuration and reinitializes providers.
// It safely handles concurrent access to shared resources.
func (sc *ScalingController) UpdateConfig(cfg config.Config) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Initialize providers with error logging
	if err := sc.initScheduleProviders(cfg, initOptions{logErrors: true}); err != nil {
		return
	}
	if err := sc.initCloudProviders(cfg, initOptions{logErrors: true}); err != nil {
		return
	}

	sc.config = cfg
	slog.Info("Controller configuration updated")
}

func (sc *ScalingController) reconcile() {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	ctx := context.Background()
	now := time.Now()

	slog.Debug("Starting reconciliation loop", "time", now)

	isWorkTime, err := sc.isWorkTime(now)
	if err != nil {
		slog.Error("Error checking work time", "error", err)
		return
	}

	slog.Debug("Work time check", "is_work_time", isWorkTime)

	for _, spec := range sc.config.NodeSpecs {
		provider := sc.providers[spec.NodePoolName]
		if provider == nil {
			slog.Warn("No provider found for node pool", "node_pool", spec.NodePoolName)
			continue
		}

		if isWorkTime {
			// During work hours, restore from saved config
			if err := provider.RestoreNodePool(ctx, spec.NodePoolName); err != nil {
				if providers.IsNoSavedStateError(err) {
					slog.Warn("No saved state found for node pool", "node_pool", spec.NodePoolName)
				} else {
					slog.Error("Error restoring node pool",
						"node_pool", spec.NodePoolName,
						"error", err,
					)
				}
			}
		} else {
			// During off hours, scale down to specified count
			if err := provider.ScaleNodePool(ctx, spec.NodePoolName, spec.OffTimeCount); err != nil {
				slog.Error("Error scaling node pool",
					"node_pool", spec.NodePoolName,
					"desired_count", spec.OffTimeCount,
					"error", err,
				)
			}
		}
	}
}

func (sc *ScalingController) isWorkTime(now time.Time) (bool, error) {
	ctx := context.Background()
	return sc.scheduler.IsWorkTime(ctx, now)
}
