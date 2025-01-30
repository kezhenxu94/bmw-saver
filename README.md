# :racing_car: BMW-Saver

BMW-Saver is a Kubernetes controller that automatically scales node pools of your development cluster based on work hours. It helps reduce costs by scaling down clusters during off-hours and restoring them during work hours.

> [!CAUTION]
> This tool actively manages node pools by scaling down nodes and draining pods. This process involves:
> - Deleting pods from nodes being scaled down
> - Modifying node pool sizes
> - Disabling cluster autoscaling during off-hours
>
> It is recommended to:
> - Only use this in development/non-production clusters
> - Ensure your applications can handle pod disruptions
> - Test thoroughly in a safe environment first

> [!CAUTION]
> **Use at your own risk. The authors are not responsible for any data loss or service disruption.**

## Features

- :clock2: Time-based node pool scaling
- :calendar: Multiple calendar integrations for holidays and off-hours, including Google Calendar, ICS Calendar (iCloud, Outlook, etc.)
- :floppy_disk: Preserves node pool configurations and restores them during work hours
- :arrows_counterclockwise: Auto-restore during work hours
- :electric_plug: Supports multiple cloud providers:
  - :white_check_mark: Google Kubernetes Engine (GKE)
  - :white_check_mark: Amazon EKS
- :memo: Live configuration updates
- :building_construction: Multi-architecture support (amd64/arm64)

## Installation

### Prerequisites

- Kubernetes cluster (GKE/EKS/AKS)
- Helm 3
- AWS credentials configured, [Pod Identity](https://repost.aws/articles/AR5ogohRSfRzCFh8ooFeq8kg/how-to-enable-amazon-eks-pod-identity-and-assign-role-to-service-account-running-workloads) is recommended
- Google Calendar API credentials (optional)

### Using Helm

#### Option 1: From OCI Registry

```bash
helm install bmw-saver oci://ghcr.io/kezhenxu94/bmw-saver-charts/bmw-saver \
  --namespace bmw-saver \
  --create-namespace \
  --version <version>
```

#### Option 2: From Local Chart

```bash
helm install bmw-saver ./charts/bmw-saver \
  --namespace bmw-saver \
  --create-namespace \
  --set image.repository=ghcr.io/kezhenxu94/bmw-saver \
  --set image.tag=latest
```

### Configuration

Create a `values.yaml` file:

```yaml
config:
  nodeSpecs:
    # GKE example:
    - nodePoolName: "my-gke-pool"
      cloudProvider: "gke"
      offTimeCount: 1  # Number of nodes during off-hours

    # EKS example:
    - nodePoolName: "my-eks-group"
      cloudProvider: "aws"
      offTimeCount: 1

  schedule:
    # Static schedule (required if not using Google Calendar)
    startTime: "09:00"        # Start time of work hours in a working day
    endTime: "17:00"          # End time of work hours in a working day
    timeZone: "Asia/Shanghai" # Time zone of the schedule
    workDays:                 # Work days in a week
      monday: true
      tuesday: true
      wednesday: true
      thursday: true
      friday: true
      saturday: false
      sunday: false

    # Optional Google Calendar integration
    googleCalendar:
      calendarId: "your-calendar-id@group.calendar.google.com"
      credentialsPath: "/etc/google/credentials.json"
      offTimeEvents: "<my name> Public Holiday"  # Search query for off-time events
      syncInterval: "1h"                        # How often to sync calendar
      cacheDays: 7                             # Days of events to cache

    # Optional ICS Calendar integration
    icsCalendar:
      url: "https://calendars.icloud.com/holidays/cn_zh.ics"  # Public holiday calendar
      workDayPatterns:                                        # Match work day events
        - ".*（班）"                                           # You know, Chinese special work days
      holidayPatterns:                                        # Match holiday events
        - ".*（休）"                                           # Chinese holidays
      syncInterval: "1h"                                      # How often to sync calendar

# Google Calendar credentials secret
googleCalendar:
  # Set to true to create a secret for Google Calendar credentials
  enabled: false
  # Base64 encoded credentials.json content
  # You can encode your file using: base64 -w 0 credentials.json
  credentials: ""
  # Or specify an existing secret name
  existingSecret: ""
  # Key in the secret that contains the credentials
  credentialsKey: "credentials.json"

env:
  # Required for AWS
  - name: EKS_CLUSTER_NAME
    value: "my-cluster-name"
```

Then install with your values:

```bash
helm install bmw-saver ./charts/bmw-saver \
  --namespace bmw-saver \
  --create-namespace \
  -f values.yaml
```

## How It Works

BMW-Saver determines work hours through:

1. During work hours:
   - Must satisfy ALL configured schedules:
     * Within static schedule time range (if configured)
     * No matching off-time events in Google Calendar (if configured)
   - Restores node pools to their saved configurations
   - Maintains autoscaling settings if previously enabled

2. During off-hours (any schedule indicates off-hours):
   - Scales down node pools to specified `offTimeCount`
   - Safely drains nodes before scaling down
   - Preserves original configuration in ConfigMaps

### Google Calendar Integration

To use Google Calendar integration:

1. Create a Google Cloud project and enable the Calendar API
2. Create a service account and download credentials.json
3. Share your calendar with the service account email
4. Base64 encode your credentials.json:
   ```bash
   base64 credentials.json > credentials.base64
   ```
5. Configure the calendar settings in values.yaml
6. Set `offTimeEvents` to match your holiday/off-time event titles

### AWS EKS Configuration

To use BMW-Saver with Amazon EKS:

1. Ensure AWS credentials are properly configured with EKS permissions
2. Set the required environment variables:
   ```yaml
   env:
     - name: EKS_CLUSTER_NAME
       value: "your-cluster-name"  # Required for EKS
   ```
3. Configure your node groups in values.yaml:
   ```yaml
   config:
     nodeSpecs:
       - nodePoolName: "my-eks-group"
         cloudProvider: "aws"
         offTimeCount: 1
   ```

## Development

### Prerequisites

- Go 1.23+
- Docker
- Kubernetes cluster (GKE/EKS/AKS)
- Helm 3

### Building

```bash
# Build binary
make build

# Build Docker image
make docker-build

# Build and push multi-arch Docker image
make docker-push HUB=ghcr.io/your-username
```

### Debugging

```bash
# Run locally
go run main.go --config config.yaml --log-level debug
```

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Donation

If this project helps you save money, please consider sponsoring me to support the development.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
