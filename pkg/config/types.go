package config

// WorkDays represents the days of the week when the schedule is active
type WorkDays struct {
	Monday    bool `yaml:"monday" default:"true"`
	Tuesday   bool `yaml:"tuesday" default:"true"`
	Wednesday bool `yaml:"wednesday" default:"true"`
	Thursday  bool `yaml:"thursday" default:"true"`
	Friday    bool `yaml:"friday" default:"true"`
	Saturday  bool `yaml:"saturday" default:"false"`
	Sunday    bool `yaml:"sunday" default:"false"`
}

// WorkSchedule represents the schedule for working hours.
// It defines when the cluster should operate at full capacity.
type WorkSchedule struct {
	// Static schedule configuration
	StartTime string    `yaml:"startTime,omitempty" default:"09:00"` // Format: "HH:MM"
	EndTime   string    `yaml:"endTime,omitempty" default:"17:00"`   // Format: "HH:MM"
	TimeZone  string    `yaml:"timeZone,omitempty" default:"UTC"`    // e.g., "America/New_York"
	WorkDays  *WorkDays `yaml:"workDays,omitempty" default:"{}"`     // Days when the schedule is active

	// Google Calendar configuration
	GoogleCalendar *GoogleCalendarConfig `yaml:"googleCalendar,omitempty"`

	// ICS Calendar configuration
	ICSCalendar *ICSCalendarConfig `yaml:"icsCalendar,omitempty"`
}

// GoogleCalendarConfig contains settings for Google Calendar integration
type GoogleCalendarConfig struct {
	// CalendarID is the ID of the Google Calendar to sync with
	CalendarID string `yaml:"calendarId"`
	// CredentialsPath is the path where the credentials file is mounted
	CredentialsPath string `yaml:"credentialsPath,omitempty" default:"/etc/google/credentials.json"`
	// OffTimeEvents is a search query for events that mark off-time hours (e.g., "<my name> PublicHoliday")
	// If any matching event is found, that time is considered off-hours
	OffTimeEvents string `yaml:"offTimeEvents,omitempty"`
	// SyncInterval is how often to refresh the event cache (default: 1h)
	SyncInterval string `yaml:"syncInterval,omitempty" default:"1h"`
	// CacheDays is how many days of events to cache (default: 7)
	CacheDays int `yaml:"cacheDays,omitempty" default:"7"`
}

// ICSCalendarConfig contains settings for ICS calendar integration
type ICSCalendarConfig struct {
	// URL is the ICS calendar URL to sync with
	URL string `yaml:"url"`
	// WorkDayPatterns is a list of patterns to match work day events
	// If any pattern matches the event summary, it's considered a work day
	WorkDayPatterns []string `yaml:"workDayPatterns,omitempty"`
	// HolidayPatterns is a list of patterns to match holiday events
	// If any pattern matches the event summary, it's considered a holiday
	HolidayPatterns []string `yaml:"holidayPatterns,omitempty"`
	// SyncInterval is how often to refresh the event cache (default: 1h)
	SyncInterval string `yaml:"syncInterval,omitempty" default:"1h"`
}

// NodeSpec represents the configuration for a node pool.
// It defines scaling behavior for a specific node pool.
type NodeSpec struct {
	OffTimeCount  int32  `yaml:"offTimeCount"`  // Number of nodes to maintain during off-hours
	NodePoolName  string `yaml:"nodePoolName"`  // Name of the node pool to manage
	CloudProvider string `yaml:"cloudProvider"` // "gke", "aws", or "azure"
}

// Config represents the overall configuration for the BMW Saver.
// It contains both scheduling and node pool specifications.
type Config struct {
	Schedule  WorkSchedule `yaml:"schedule"`
	NodeSpecs []NodeSpec   `yaml:"nodeSpecs"`
}
