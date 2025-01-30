package schedule

import (
	"context"
	"fmt"
	"time"
)

// StaticProvider is a simple schedule provider that uses fixed start and end times
type StaticProvider struct {
	StartTime string
	EndTime   string
	TimeZone  string
	WorkDays  map[time.Weekday]bool
}

// NewStaticProvider creates a new static schedule provider
func NewStaticProvider(startTime, endTime, timeZone string, workDays map[time.Weekday]bool) *StaticProvider {
	if workDays == nil {
		// Default to Monday-Friday if not specified
		workDays = map[time.Weekday]bool{
			time.Monday:    true,
			time.Tuesday:   true,
			time.Wednesday: true,
			time.Thursday:  true,
			time.Friday:    true,
		}
	}

	return &StaticProvider{
		StartTime: startTime,
		EndTime:   endTime,
		TimeZone:  timeZone,
		WorkDays:  workDays,
	}
}

// IsWorkTime checks if the current time is within the working hours
func (p *StaticProvider) IsWorkTime(ctx context.Context, now time.Time) (bool, error) {
	location, err := time.LoadLocation(p.TimeZone)
	if err != nil {
		return false, err
	}

	nowInTz := now.In(location)

	// Check if current day is a work day
	if !p.WorkDays[nowInTz.Weekday()] {
		return false, nil
	}

	startTime, err := time.ParseInLocation("15:04", p.StartTime, location)
	if err != nil {
		return false, err
	}

	endTime, err := time.ParseInLocation("15:04", p.EndTime, location)
	if err != nil {
		return false, err
	}

	startTime = time.Date(nowInTz.Year(), nowInTz.Month(), nowInTz.Day(),
		startTime.Hour(), startTime.Minute(), 0, 0, location)
	endTime = time.Date(nowInTz.Year(), nowInTz.Month(), nowInTz.Day(),
		endTime.Hour(), endTime.Minute(), 0, 0, location)

	return nowInTz.After(startTime) && nowInTz.Before(endTime), nil
}

// String returns a string representation of the StaticProvider
func (p *StaticProvider) String() string {
	workDays := make([]string, 0, len(p.WorkDays))
	for day, enabled := range p.WorkDays {
		if enabled {
			workDays = append(workDays, day.String())
		}
	}
	return fmt.Sprintf("StaticProvider{startTime: %s, endTime: %s, timeZone: %s, workDays: %v}",
		p.StartTime,
		p.EndTime,
		p.TimeZone,
		workDays)
}
