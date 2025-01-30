package schedule

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"log/slog"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type cachedEvent struct {
	Start time.Time
	End   time.Time
}

type eventCache struct {
	events    map[string][]cachedEvent // date string -> events
	lastSync  time.Time
	syncMutex sync.RWMutex
}

// GoogleCalendarProvider is a schedule provider that uses Google Calendar
type GoogleCalendarProvider struct {
	service       *calendar.Service
	calendarID    string
	offTimeEvents string
	cache         *eventCache
	// Configurable settings
	syncInterval time.Duration // How often to refresh the cache
	cacheDays    int           // How many days of events to cache
}

// NewGoogleCalendarProvider creates a new GoogleCalendarProvider
func NewGoogleCalendarProvider(credentialsPath, calendarID string, offTimeEvents string, syncInterval time.Duration, cacheDays int) (*GoogleCalendarProvider, error) {
	ctx := context.Background()
	if !filepath.IsAbs(credentialsPath) {
		return nil, fmt.Errorf("credentials path must be absolute: %s", credentialsPath)
	}
	b, err := os.ReadFile(filepath.Clean(credentialsPath))
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %v", err)
	}

	config, err := google.JWTConfigFromJSON(b, calendar.CalendarReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %v", err)
	}

	service, err := calendar.NewService(ctx, option.WithHTTPClient(config.Client(ctx)))
	if err != nil {
		return nil, fmt.Errorf("failed to create calendar service: %v", err)
	}

	provider := &GoogleCalendarProvider{
		service:       service,
		calendarID:    calendarID,
		offTimeEvents: offTimeEvents,
		cache: &eventCache{
			events: make(map[string][]cachedEvent),
		},
		syncInterval: syncInterval,
		cacheDays:    cacheDays,
	}

	// Initial sync
	if err := provider.syncEvents(ctx); err != nil {
		return nil, fmt.Errorf("failed initial event sync: %v", err)
	}

	// Start background sync
	go provider.backgroundSync(context.Background())

	return provider, nil
}

func (p *GoogleCalendarProvider) backgroundSync(ctx context.Context) {
	ticker := time.NewTicker(p.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.syncEvents(ctx); err != nil {
				slog.Error("Failed to sync calendar events", "error", err)
			}
		}
	}
}

func (p *GoogleCalendarProvider) syncEvents(ctx context.Context) error {
	p.cache.syncMutex.Lock()
	defer p.cache.syncMutex.Unlock()

	// Clear existing cache
	p.cache.events = make(map[string][]cachedEvent)

	// Calculate time range
	now := time.Now()
	timeMin := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Format(time.RFC3339)
	timeMax := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, p.cacheDays).Format(time.RFC3339)
	slog.Info("Syncing calendar events", "timeMin", timeMin, "timeMax", timeMax)

	// Build query for all off-time events
	query := p.offTimeEvents
	if query == "" {
		return nil
	}

	events, err := p.service.Events.List(p.calendarID).
		TimeMin(timeMin).
		TimeMax(timeMax).
		Q(query).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return fmt.Errorf("failed to list calendar events: %v", err)
	}

	// Process and cache events
	for _, event := range events.Items {
		var start, end time.Time
		var err error

		if event.Start.DateTime != "" {
			start, err = time.Parse(time.RFC3339, event.Start.DateTime)
			if err != nil {
				slog.Warn("Failed to parse event start time", "event", event.Summary, "error", err)
				continue
			}
		} else if event.Start.Date != "" {
			// Handle all-day events
			start, err = time.Parse("2006-01-02", event.Start.Date)
			if err != nil {
				slog.Warn("Failed to parse event start date", "event", event.Summary, "error", err)
				continue
			}
		} else {
			slog.Warn("Event has no start time or date", "event", event.Summary)
			continue
		}

		if event.End.DateTime != "" {
			end, err = time.Parse(time.RFC3339, event.End.DateTime)
			if err != nil {
				slog.Warn("Failed to parse event end time", "event", event.Summary, "error", err)
				continue
			}
		} else if event.End.Date != "" {
			// Handle all-day events
			end, err = time.Parse("2006-01-02", event.End.Date)
			if err != nil {
				slog.Warn("Failed to parse event end date", "event", event.Summary, "error", err)
				continue
			}
		} else {
			slog.Warn("Event has no end time or date", "event", event.Summary)
			continue
		}

		// Create event entry
		entry := cachedEvent{
			Start: start,
			End:   end,
		}

		// Store event for each day in its range
		for current := start; current.Before(end); current = current.AddDate(0, 0, 1) {
			dateKey := current.Format("2006-01-02")
			p.cache.events[dateKey] = append(p.cache.events[dateKey], entry)
		}
	}

	p.cache.lastSync = time.Now()
	slog.Info("Google Calendar events synced successfully",
		"events_count", len(events.Items),
	)
	return nil
}

// IsWorkTime checks if the given time is within working hours
func (p *GoogleCalendarProvider) IsWorkTime(ctx context.Context, t time.Time) (bool, error) {
	p.cache.syncMutex.RLock()
	defer p.cache.syncMutex.RUnlock()

	// Check if we have events for this date
	dateKey := t.Format("2006-01-02")
	events, ok := p.cache.events[dateKey]
	if !ok {
		// If no events for this date, consider it work time
		return true, nil
	}

	// Check if time falls within any off-time event
	for _, event := range events {
		// Event end dates are exclusive, so we check if time is >= start and < end
		if !t.Before(event.Start) && t.Before(event.End) {
			return false, nil
		}
	}

	// No off-time events found for this time
	return true, nil
}

// String returns a string representation of the GoogleCalendarProvider
func (p *GoogleCalendarProvider) String() string {
	return fmt.Sprintf("GoogleCalendarProvider{calendarId: %s, offTimeEvents: %v, syncInterval: %v, cacheDays: %d, cacheSize: %d}",
		p.calendarID,
		p.offTimeEvents,
		p.syncInterval,
		p.cacheDays,
		len(p.cache.events))
}
