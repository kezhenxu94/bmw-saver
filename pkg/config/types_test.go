package config

import (
	"testing"
)

func TestDefaultValues(t *testing.T) {
	// Test WorkSchedule defaults
	schedule := &WorkSchedule{}
	setDefaults(schedule)

	// Check WorkSchedule defaults
	if schedule.StartTime != "09:00" {
		t.Errorf("expected StartTime default to be '09:00', got '%s'", schedule.StartTime)
	}
	if schedule.EndTime != "17:00" {
		t.Errorf("expected EndTime default to be '17:00', got '%s'", schedule.EndTime)
	}
	if schedule.TimeZone != "UTC" {
		t.Errorf("expected TimeZone default to be 'UTC', got '%s'", schedule.TimeZone)
	}

	// Check that WorkDays was initialized
	if schedule.WorkDays == nil {
		t.Fatal("expected WorkDays to be initialized")
	}

	// Check WorkDays defaults
	workDays := schedule.WorkDays
	tests := []struct {
		day    string
		value  bool
		actual bool
	}{
		{"Monday", true, workDays.Monday},
		{"Tuesday", true, workDays.Tuesday},
		{"Wednesday", true, workDays.Wednesday},
		{"Thursday", true, workDays.Thursday},
		{"Friday", true, workDays.Friday},
		{"Saturday", false, workDays.Saturday},
		{"Sunday", false, workDays.Sunday},
	}

	for _, tt := range tests {
		if tt.actual != tt.value {
			t.Errorf("expected %s default to be %v, got %v", tt.day, tt.value, tt.actual)
		}
	}

	// Check GoogleCalendarConfig defaults
	if schedule.GoogleCalendar != nil {
		if schedule.GoogleCalendar.CredentialsPath != "/etc/google/credentials.json" {
			t.Errorf("expected CredentialsPath default to be '/etc/google/credentials.json', got '%s'",
				schedule.GoogleCalendar.CredentialsPath)
		}
	}
}
