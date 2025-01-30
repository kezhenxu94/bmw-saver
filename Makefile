.PHONY: build docker-build deploy debug clean docker-push

APP_NAME := bmw-saver
HUB ?= docker.io/library
TAG ?= $(shell git rev-parse --short HEAD)
DOCKER_IMAGE := $(HUB)/$(APP_NAME):$(TAG)
NAMESPACE ?= bmw-saver
PLATFORM ?= linux/amd64,linux/arm64
GOBIN := $(shell go env GOPATH)/bin
GOLANGCI_LINT_VERSION ?= v1.63.4
GOLANGCI_LINT_BIN := $(GOBIN)/golangci-lint
COVERAGE_DIR := coverage
COVERAGE_PROFILE := $(COVERAGE_DIR)/coverage.out
COVERAGE_HTML := $(COVERAGE_DIR)/coverage.html

build:
	CGO_ENABLED=0 go build -o bin/$(APP_NAME)

$(GOLANGCI_LINT_BIN):
	@echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOBIN) $(GOLANGCI_LINT_VERSION); \

lint: $(GOLANGCI_LINT_BIN)
	$(GOLANGCI_LINT_BIN) run --timeout=5m

lint-fix: $(GOLANGCI_LINT_BIN)
	$(GOLANGCI_LINT_BIN) run --fix --timeout=5m

test:
	@mkdir -p $(COVERAGE_DIR)
	go test -race -coverprofile=$(COVERAGE_PROFILE) -covermode=atomic ./...
	go tool cover -html=$(COVERAGE_PROFILE) -o $(COVERAGE_HTML)
	@echo "Coverage report generated at $(COVERAGE_HTML)"

test-short:
	go test -short ./...

clean-test:
	rm -rf $(COVERAGE_DIR)

docker-build:
	docker buildx create --use --name builder --driver docker-container --bootstrap || true
	docker buildx build --platform $(PLATFORM) -t $(DOCKER_IMAGE) .

docker-push: 
	docker buildx create --use --name builder --driver docker-container --bootstrap || true
	docker buildx build --platform $(PLATFORM) -t $(DOCKER_IMAGE) --push .

deploy-gke:
	helm upgrade --install $(APP_NAME) ./charts/$(APP_NAME) \
	--namespace $(NAMESPACE) \
	--create-namespace \
	--set config.nodeSpecs[0].cloudProvider=gke \
	--set config.nodeSpecs[0].nodePoolName=$(NODE_POOL_NAME) \
	--set config.nodeSpecs[0].offTimeCount=$(OFF_TIME_COUNT) \
	--set config.schedule.icsCalendar.url="https://calendars.icloud.com/holidays/cn_zh.ics" \
	--set config.schedule.icsCalendar.workDayPatterns[0]=".*（班）" \
	--set config.schedule.icsCalendar.holidayPatterns[0]=".*（休）" \
	--set config.schedule.icsCalendar.syncInterval="1h" \
	--set image.repository=$(HUB)/$(APP_NAME) \
	--set image.tag=$(TAG) \
	--set image.pullPolicy=Always

deploy-aws:
	helm upgrade --install $(APP_NAME) ./charts/$(APP_NAME) \
	--namespace $(NAMESPACE) \
	--create-namespace \
	--set config.nodeSpecs[0].cloudProvider=aws \
	--set config.nodeSpecs[0].nodePoolName=$(NODE_POOL_NAME) \
	--set config.nodeSpecs[0].offTimeCount=$(OFF_TIME_COUNT) \
	--set config.schedule.icsCalendar.url="https://calendars.icloud.com/holidays/cn_zh.ics" \
	--set config.schedule.icsCalendar.workDayPatterns[0]=".*（班）" \
	--set config.schedule.icsCalendar.holidayPatterns[0]=".*（休）" \
	--set config.schedule.icsCalendar.syncInterval="1h" \
	--set env[0].name=EKS_CLUSTER_NAME \
	--set env[0].value=$(EKS_CLUSTER_NAME) \
	--set image.repository=$(HUB)/$(APP_NAME) \
	--set image.tag=$(TAG) \
	--set image.pullPolicy=Always

clean: clean-test
	rm -rf bin/
	helm uninstall $(APP_NAME) -n $(NAMESPACE) --ignore-not-found
	kubectl delete namespace $(NAMESPACE) --ignore-not-found 
