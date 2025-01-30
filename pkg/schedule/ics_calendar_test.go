package schedule

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "embed"
)

//go:embed testdata/cn_zh.ics
var cnZhIcs []byte

func TestICSCalendarProvider_IsWorkTime(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(cnZhIcs)
	}))
	defer server.Close()

	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("Failed to load location: %v", err)
	}

	tests := []struct {
		name            string
		workPatterns    []string
		holidayPatterns []string
		checkTime       time.Time
		want            bool
		wantErr         bool
	}{
		{
			name:            "Chinese New Year Holiday",
			workPatterns:    []string{".*（班）"},
			holidayPatterns: []string{".*（休）"},
			checkTime:       time.Date(2023, time.January, 22, 10, 0, 0, 0, location),
			want:            false,
		},
		{
			name:            "Holiday Start Time",
			workPatterns:    []string{".*（班）"},
			holidayPatterns: []string{".*（休）"},
			checkTime:       time.Date(2023, time.April, 29, 0, 0, 0, 0, location),
			want:            false,
		},
		{
			name:            "Holiday End Time",
			workPatterns:    []string{".*（班）"},
			holidayPatterns: []string{".*（休）"},
			checkTime:       time.Date(2023, time.May, 4, 0, 0, 0, 0, location),
			want:            true,
		},
		{
			name:            "Work Day",
			workPatterns:    []string{".*（班）"},
			holidayPatterns: []string{".*（休）"},
			checkTime:       time.Date(2023, time.January, 28, 10, 0, 0, 0, location),
			want:            true,
		},
		{
			name:            "No Patterns - Default Work Time",
			workPatterns:    nil,
			holidayPatterns: nil,
			checkTime:       time.Date(2024, time.February, 10, 10, 0, 0, 0, location),
			want:            true,
		},
		{
			name:            "One Day Holiday",
			workPatterns:    []string{".*（班）"},
			holidayPatterns: []string{".*（休）"},
			checkTime:       time.Date(2023, time.April, 5, 12, 0, 0, 0, location),
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewICSCalendarProvider(
				server.URL,
				1*time.Hour,
				tt.workPatterns,
				tt.holidayPatterns,
			)
			if err != nil {
				t.Fatalf("Failed to create provider: %v", err)
			}

			got, err := provider.IsWorkTime(context.Background(), tt.checkTime)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsWorkTime() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IsWorkTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestICSCalendarProvider_InvalidPatterns(t *testing.T) {
	tests := []struct {
		name            string
		workPatterns    []string
		holidayPatterns []string
		wantErrContains string
	}{
		{
			name:            "Invalid Work Pattern",
			workPatterns:    []string{"[invalid"},
			holidayPatterns: []string{},
			wantErrContains: "invalid work day pattern",
		},
		{
			name:            "Invalid Holiday Pattern",
			workPatterns:    []string{},
			holidayPatterns: []string{"[invalid"},
			wantErrContains: "invalid holiday pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewICSCalendarProvider(
				"http://example.com",
				1*time.Hour,
				tt.workPatterns,
				tt.holidayPatterns,
			)
			if err == nil {
				t.Error("Expected error, got nil")
				return
			}
			if !contains(err.Error(), tt.wantErrContains) {
				t.Errorf("Expected error containing %q, got %v", tt.wantErrContains, err)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(substr)] == substr
}
