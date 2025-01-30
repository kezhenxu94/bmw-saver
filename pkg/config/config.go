package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"

	"sigs.k8s.io/yaml"
)

// setDefaults sets default values for a struct using 'default' tags
func setDefaults(v interface{}) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return
	}

	rv = rv.Elem()
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rv.Field(i)
		if !field.CanSet() {
			continue
		}

		tag := rt.Field(i).Tag.Get("default")
		if tag == "{}" {
			// If it's a struct pointer, initialize it and set its defaults
			if field.Kind() == reflect.Ptr && field.IsNil() && field.Type().Elem().Kind() == reflect.Struct {
				field.Set(reflect.New(field.Type().Elem()))
				setDefaults(field.Interface())
			}
			continue
		}

		switch field.Kind() {
		case reflect.String:
			if field.String() == "" {
				field.SetString(tag)
			}
		case reflect.Bool:
			if !field.Bool() {
				val, _ := strconv.ParseBool(tag)
				field.SetBool(val)
			}
		}
	}
}

// ReadConfigFromBytes parses and validates config from raw bytes
func ReadConfigFromBytes(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("failed to parse config: %v", err)
	}

	// Initialize WorkDays if not set
	if cfg.Schedule.WorkDays == nil {
		cfg.Schedule.WorkDays = &WorkDays{}
	}

	// Set default values
	setDefaults(&cfg.Schedule)
	setDefaults(cfg.Schedule.WorkDays)

	// Validate that at least one schedule provider is configured
	if !hasValidScheduleConfig(cfg.Schedule) {
		return Config{}, fmt.Errorf("no valid schedule configuration provided")
	}

	// Validate individual configurations if present
	if hasStaticSchedule(cfg.Schedule) {
		if err := validateStaticSchedule(cfg.Schedule); err != nil {
			return Config{}, err
		}
	}
	if cfg.Schedule.GoogleCalendar != nil {
		if err := validateGoogleCalendarSchedule(cfg.Schedule); err != nil {
			return Config{}, err
		}
	}

	// Validate node specs
	for i, spec := range cfg.NodeSpecs {
		if err := validateNodeSpec(spec, i); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}

// ReadConfig reads config from a file path
func ReadConfig(path string) (Config, error) {
	if !filepath.IsAbs(path) {
		return Config{}, fmt.Errorf("config path must be absolute: %s", path)
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %v", err)
	}

	return ReadConfigFromBytes(data)
}

func validateStaticSchedule(schedule WorkSchedule) error {
	if schedule.StartTime == "" {
		return fmt.Errorf("start time is required for static schedule")
	}
	if schedule.EndTime == "" {
		return fmt.Errorf("end time is required for static schedule")
	}
	if schedule.TimeZone == "" {
		return fmt.Errorf("time zone is required for static schedule")
	}
	return nil
}

func validateGoogleCalendarSchedule(schedule WorkSchedule) error {
	if schedule.GoogleCalendar == nil {
		return fmt.Errorf("google calendar configuration is required when using google_calendar provider")
	}
	if schedule.GoogleCalendar.CalendarID == "" {
		return fmt.Errorf("calendar ID is required for google calendar schedule")
	}
	if schedule.GoogleCalendar.CredentialsPath == "" {
		return fmt.Errorf("credentials file is required for google calendar schedule")
	}
	return nil
}

func validateNodeSpec(spec NodeSpec, index int) error {
	if spec.NodePoolName == "" {
		return fmt.Errorf("node pool name is required for spec %d", index)
	}
	if spec.CloudProvider == "" {
		return fmt.Errorf("cloud provider is required for spec %d", index)
	}
	if spec.OffTimeCount < 0 {
		return fmt.Errorf("invalid off-time node count for spec %d", index)
	}
	return nil
}

func hasValidScheduleConfig(schedule WorkSchedule) bool {
	return hasStaticSchedule(schedule) || schedule.GoogleCalendar != nil
}

func hasStaticSchedule(schedule WorkSchedule) bool {
	return schedule.StartTime != "" && schedule.EndTime != "" && schedule.TimeZone != ""
}
