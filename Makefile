.PHONY: setup build up down logs test docker-test format fmt
setup:
	cd backend && go run ./cmd/devstack secrets ../.local/dev
build:
	docker compose build db api frontend
up:
	docker compose up -d --wait
down:
	docker compose down
logs:
	docker compose logs --tail=100
test:
	cd backend && go vet ./... && go test ./...
docker-test:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/Test-DockerCore.ps1
format:
	cd backend && gofmt -w cmd internal
	npx prettier --write .
fmt: format
