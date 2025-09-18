# Makefile for load generator

.PHONY: build up down logs scale clean help

# Default configuration
REPLICAS ?= 5
TARGET_URL ?= https://pinetswap.net/piswap.html
NUM_WORKERS ?= 400

build:
	docker-compose build

up:
	docker-compose up -d --scale load-generator=$(REPLICAS)

down:
	docker-compose down

logs:
	docker-compose logs -f

logs-single:
	docker-compose logs -f load-generator

scale:
	docker-compose up -d --scale load-generator=$(REPLICAS)

clean:
	docker-compose down -v --remove-orphans
	docker system prune -f

# Run with custom settings
run-custom:
	TARGET_URL=$(TARGET_URL) NUM_WORKERS=$(NUM_WORKERS) docker-compose up -d --scale load-generator=$(REPLICAS)

# Run manual services (different configurations)
run-manual:
	docker-compose --profile manual up -d

# Monitor resource usage
monitor:
	docker stats

help:
	@echo "Available targets:"
	@echo "  build      - Build the Docker image"
	@echo "  up         - Start services with default replicas ($(REPLICAS))"
	@echo "  down       - Stop all services"
	@echo "  logs       - Show logs from all services"
	@echo "  logs-single - Show logs from load-generator service only"
	@echo "  scale      - Scale load-generator to REPLICAS count"
	@echo "  clean      - Stop services and clean up"
	@echo "  run-custom - Run with custom TARGET_URL and NUM_WORKERS"
	@echo "  run-manual - Run manual profile services"
	@echo "  monitor    - Show Docker container resource usage"
	@echo ""
	@echo "Environment variables:"
	@echo "  REPLICAS   - Number of replicas (default: $(REPLICAS))"
	@echo "  TARGET_URL - Target URL (default: $(TARGET_URL))"
	@echo "  NUM_WORKERS - Workers per replica (default: $(NUM_WORKERS))"
	@echo ""
	@echo "Examples:"
	@echo "  make up REPLICAS=10"
	@echo "  make run-custom TARGET_URL=https://example.com NUM_WORKERS=200 REPLICAS=3"