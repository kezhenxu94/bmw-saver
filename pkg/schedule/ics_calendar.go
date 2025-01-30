package schedule

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	ics "github.com/arran4/golang-ical"
)

// httpClient interface allows mocking http.Client in tests
type httpClient interface {
	Get(url string) (*http.Response, error)
}

// ICSCalendarProvider is a schedule provider that uses ICS calendar URLs
type ICSCalendarProvider struct {
	url             string
	syncInterval    time.Duration
	workPatterns    []*regexp.Regexp
	holidayPatterns []*regexp.Regexp
	events          map[string][]calendarEvent
	mu              sync.RWMutex
	client          httpClient
}

type calendarEvent struct {
	Start   time.Time
	End     time.Time
	Summary string
}

// NewICSCalendarProvider creates a new ICS calendar provider
func NewICSCalendarProvider(url string, syncInterval time.Duration, workDayPatterns, holidayPatterns []string) (*ICSCalendarProvider, error) {
	var workDayEventPatterns, holidayEventPatterns []*regexp.Regexp

	// Compile work day patterns
	for _, pattern := range workDayPatterns {
		regex, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid work day pattern %q: %v", pattern, err)
		}
		workDayEventPatterns = append(workDayEventPatterns, regex)
	}

	// Compile holiday patterns
	for _, pattern := range holidayPatterns {
		regex, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid holiday pattern %q: %v", pattern, err)
		}
		holidayEventPatterns = append(holidayEventPatterns, regex)
	}

	provider := &ICSCalendarProvider{
		url:             url,
		syncInterval:    syncInterval,
		workPatterns:    workDayEventPatterns,
		holidayPatterns: holidayEventPatterns,
		events:          make(map[string][]calendarEvent),
		client:          &http.Client{},
	}

	// Initial sync
	if err := provider.syncEvents(context.Background()); err != nil {
		return nil, fmt.Errorf("failed initial event sync: %v", err)
	}

	// Start background sync
	go provider.backgroundSync(context.Background())

	return provider, nil
}

func (p *ICSCalendarProvider) backgroundSync(ctx context.Context) {
	ticker := time.NewTicker(p.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.syncEvents(ctx); err != nil {
				slog.Error("Failed to sync ICS calendar events", "error", err)
			}
		}
	}
}

func (p *ICSCalendarProvider) syncEvents(ctx context.Context) error {
	// Fetch ICS calendar
	resp, err := p.client.Get(p.url)
	if err != nil {
		return fmt.Errorf("failed to fetch ICS calendar: %v", err)
	}
	defer func() {
		if e := resp.Body.Close(); e != nil {
			slog.Error("Failed to close ICS calendar response body", "error", e)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	calendar, err := ics.ParseCalendar(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to parse ICS calendar: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	slog.Info("ICS calendar events synced successfully", "events_count", len(calendar.Events()))

	// Clear existing cache
	p.events = make(map[string][]calendarEvent)

	for _, event := range calendar.Events() {
		start, err := event.GetStartAt()
		if err != nil {
			slog.Warn("Failed to parse event start time", "error", err)
			continue
		}

		end, err := event.GetEndAt()
		if err != nil {
			// If no end time specified, treat it as a one-day event
			// End time is exclusive, so add one day to start time
			end = start.AddDate(0, 0, 1)
			slog.Debug("No end time specified, treating as one-day event",
				"summary", event.GetProperty(ics.ComponentPropertySummary),
				"start", start,
				"end", end,
			)
		}

		summary := event.GetProperty(ics.ComponentPropertySummary)
		if summary == nil {
			slog.Debug("Event has no summary", "start", start, "end", end)
			continue
		}

		// Create event entry
		entry := calendarEvent{
			Start:   start,
			End:     end,
			Summary: summary.Value,
		}

		// Store event for each day in its range
		for current := start; current.Before(end); current = current.AddDate(0, 0, 1) {
			dateKey := current.Format("2006-01-02")
			p.events[dateKey] = append(p.events[dateKey], entry)
		}
	}

	slog.Info("ICS calendar events synced successfully",
		"events_count", len(calendar.Events()),
	)
	return nil
}

// IsWorkTime checks if the given time is within working hours
func (p *ICSCalendarProvider) IsWorkTime(ctx context.Context, t time.Time) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Check if we have events for this date
	dateKey := t.Format("2006-01-02")
	events, ok := p.events[dateKey]
	if !ok {
		// If no events for this date, consider it work time
		return true, nil
	}

	for _, event := range events {
		// ICS event end dates are exclusive, so we check if time is >= start and < end
		if !t.Before(event.Start) && t.Before(event.End) {
			// Check if it's a holiday
			for _, pattern := range p.holidayPatterns {
				if pattern.MatchString(event.Summary) {
					return false, nil
				}
			}
			// Check if it's a work day
			for _, pattern := range p.workPatterns {
				if pattern.MatchString(event.Summary) {
					return true, nil
				}
			}
		}
	}

	// No matching events found, default to work time
	return true, nil
}

// String returns a string representation of the ICSCalendarProvider
func (p *ICSCalendarProvider) String() string {
	return fmt.Sprintf("ICSCalendarProvider{url: %s, syncInterval: %v, workPatterns: %d, holidayPatterns: %d, events: %d}",
		p.url,
		p.syncInterval,
		len(p.workPatterns),
		len(p.holidayPatterns),
		len(p.events))
}
