.PHONY: help build run test clean dev up stop migrate-up migrate-down docker-build docker-run swagger swagger-tools lint fmt vet

# Variables
APP_NAME=messenger
VERSION=$(shell git describe --tags --always --dirty)
BUILD_DIR=./bin
MAIN_PATH=./cmd/api
DOCKER_IMAGE=$(APP_NAME):$(VERSION)

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet
SWAG_VERSION ?= v1.16.6
SWAG_BIN = $(abspath $(BUILD_DIR))/swag$(TOOL_EXE)

# Load environment variables
ifneq (,$(wildcard ./.env))
    include .env
    export
endif

# Detect OS
ifeq ($(OS),Windows_NT)
    DETECTED_OS := Windows
    TOOL_EXE := .exe
    BUILD_TIME := $(shell powershell -NoProfile -Command "Get-Date -UFormat '%Y-%m-%d_%H:%M:%S'")
    SLEEP_CMD := powershell -Command "Start-Sleep -Seconds 3"
else
    DETECTED_OS := $(shell uname -s)
    TOOL_EXE :=
    BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
    SLEEP_CMD := sleep 3
endif

# Build flags
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

help: ## Display this help screen
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the application
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(APP_NAME)"

run: ## Run the application
	@echo "Running $(APP_NAME)..."
	$(GOCMD) run $(MAIN_PATH)/main.go

dev: ## Run with hot reload (requires air)
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "air not found. Install it with: go install github.com/air-verse/air@latest"; \
		$(GOCMD) run $(MAIN_PATH)/main.go; \
	fi

up: ## Start infra (DB, Redis, etc) and run the app
	@echo "Starting infrastructure containers..."
	docker-compose up -d messenger-postgres messenger-redis messenger-minio messenger-rabbitmq
	@echo "Waiting for DB to be ready..."
	@$(SLEEP_CMD)
	@echo "Starting application..."
	$(MAKE) dev

stop: ## Stop all infrastructure containers
	docker-compose stop messenger-postgres messenger-redis messenger-minio messenger-rabbitmq


test: ## Run tests
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...

test-coverage: test ## Run tests with coverage report
	@echo "Generating coverage report..."
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

bench: ## Run benchmarks
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html
	$(GOCLEAN)

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

fmt: ## Format code
	@echo "Formatting code..."
	$(GOFMT) ./...

vet: ## Run go vet
	@echo "Running go vet..."
	$(GOVET) ./...

lint: ## Run golangci-lint
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found. Install it from https://golangci-lint.run/usage/install/"; \
		exit 1; \
	fi

migrate-up: ## Run database migrations up
	@echo "Running migrations up..."
	@if command -v goose > /dev/null; then \
		cd db/migrations && goose postgres "host=$(POSTGRES_HOST) port=$(POSTGRES_PORT) user=$(POSTGRES_USER) password=$(POSTGRES_PASSWORD) dbname=$(POSTGRES_DB) sslmode=$(POSTGRES_SSL_MODE)" up; \
	else \
		echo "goose not found. Install it with: go install github.com/pressly/goose/v3/cmd/goose@latest"; \
		exit 1; \
	fi

migrate-down: ## Run database migrations down
	@echo "Running migrations down..."
	@if command -v goose > /dev/null; then \
		cd db/migrations && goose postgres "host=$(POSTGRES_HOST) port=$(POSTGRES_PORT) user=$(POSTGRES_USER) password=$(POSTGRES_PASSWORD) dbname=$(POSTGRES_DB) sslmode=$(POSTGRES_SSL_MODE)" down; \
	else \
		echo "goose not found. Install it with: go install github.com/pressly/goose/v3/cmd/goose@latest"; \
		exit 1; \
	fi

migrate-status: ## Show migration status
	@echo "Migration status..."
	@if command -v goose > /dev/null; then \
		cd db/migrations && goose postgres "host=localhost port=5432 user=postgres password=postgres dbname=messenger sslmode=disable" status; \
	else \
		echo "goose not found. Install it with: go install github.com/pressly/goose/v3/cmd/goose@latest"; \
		exit 1; \
	fi

migrate-create: ## Create a new migration (usage: make migrate-create name=create_users)
	@if [ -z "$(name)" ]; then \
		echo "Error: name is required. Usage: make migrate-create name=create_users"; \
		exit 1; \
	fi
	@if command -v goose > /dev/null; then \
		cd db/migrations && goose create $(name) sql; \
	else \
		echo "goose not found. Install it with: go install github.com/pressly/goose/v3/cmd/goose@latest"; \
		exit 1; \
	fi

$(SWAG_BIN):
	@echo "Installing swag $(SWAG_VERSION) into $(BUILD_DIR)..."
	@mkdir -p "$(BUILD_DIR)"
	GOBIN="$(abspath $(BUILD_DIR))" $(GOCMD) install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)

swagger-tools: $(SWAG_BIN) ## Install the local Swagger generator

swagger: swagger-tools ## Generate swagger documentation
	@echo "Generating swagger docs..."
	"$(SWAG_BIN)" init -g cmd/api/main.go -o docs

docker-build: ## Build docker image
	@echo "Building docker image $(DOCKER_IMAGE)..."
	docker build -t $(DOCKER_IMAGE) .
	docker tag $(DOCKER_IMAGE) $(APP_NAME):latest

docker-run: ## Run docker container
	@echo "Running docker container..."
	docker run -p 8080:8080 --env-file .env $(APP_NAME):latest

docker-compose-up: ## Start all services with docker-compose
	@echo "Starting services with docker-compose..."
	docker-compose up -d

docker-compose-down: ## Stop all services
	@echo "Stopping services..."
	docker-compose down

docker-compose-logs: ## Show docker-compose logs
	docker-compose logs -f

db-create: ## Create database
	@echo "Creating database..."
	docker exec -it postgres-messenger createdb -U postgres messenger || echo "Database might already exist"

db-drop: ## Drop database
	@echo "Dropping database..."
	docker exec -it postgres-messenger dropdb -U postgres messenger

install-tools: ## Install development tools
	@echo "Installing development tools..."
	go install github.com/air-verse/air@latest
	go install github.com/pressly/goose/v3/cmd/goose@latest
	$(MAKE) swagger-tools
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "Tools installed successfully!"

setup: install-tools deps ## Initial project setup
	@echo "Setting up project..."
	@cp -n .env.example .env 2>/dev/null || echo ".env already exists"
	@echo "Setup complete! Edit .env file with your configuration."

# Default target
.DEFAULT_GOAL := help
