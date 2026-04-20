SHELL := /bin/bash

REGISTRY ?= ghcr.io
IMAGE_BASE ?= nulzo/trader
BACKEND_IMAGE  := $(REGISTRY)/$(IMAGE_BASE)-backend
FRONTEND_IMAGE := $(REGISTRY)/$(IMAGE_BASE)-frontend
TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
PLATFORM ?= linux/arm64

.PHONY: help
help: ## Show this help
	@awk 'BEGIN{FS=":.*##"; printf "\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/{printf "  \033[36m%-24s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)

##@ Dev
.PHONY: backend-run frontend-run backend-test backend-vet
backend-run: ## Run backend locally
	cd backend && go run ./cmd/trader

frontend-run: ## Run frontend vite dev
	cd frontend && bun run dev

backend-test: ## Run backend go tests
	cd backend && go test ./...

backend-vet: ## go vet the backend
	cd backend && go vet ./...

##@ Docker (local)
.PHONY: compose-up compose-down compose-build
compose-up: ## Local stack via docker compose
	docker compose -f infra/docker-compose.yml up --build

compose-down: ## Tear down local stack
	docker compose -f infra/docker-compose.yml down

compose-build: ## Build compose images
	docker compose -f infra/docker-compose.yml build

##@ Docker (arm64 for Pi)
.PHONY: buildx-setup image-backend image-frontend push-backend push-frontend
buildx-setup: ## Ensure buildx + QEMU
	docker buildx create --use --name trader-builder 2>/dev/null || true
	docker run --rm --privileged tonistiigi/binfmt --install all

image-backend: ## Build backend image for Pi
	docker buildx build --platform $(PLATFORM) \
		-t $(BACKEND_IMAGE):$(TAG) -t $(BACKEND_IMAGE):latest \
		-f backend/Dockerfile backend --load

image-frontend: ## Build frontend image for Pi
	docker buildx build --platform $(PLATFORM) --build-arg VITE_API_URL= \
		-t $(FRONTEND_IMAGE):$(TAG) -t $(FRONTEND_IMAGE):latest \
		-f frontend/Dockerfile frontend --load

push-backend: ## Build+push backend
	docker buildx build --platform $(PLATFORM) \
		-t $(BACKEND_IMAGE):$(TAG) -t $(BACKEND_IMAGE):latest \
		-f backend/Dockerfile backend --push

push-frontend: ## Build+push frontend
	docker buildx build --platform $(PLATFORM) --build-arg VITE_API_URL= \
		-t $(FRONTEND_IMAGE):$(TAG) -t $(FRONTEND_IMAGE):latest \
		-f frontend/Dockerfile frontend --push
