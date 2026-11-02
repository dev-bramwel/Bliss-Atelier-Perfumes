.PHONY: setup build up down logs test docker-test format fmt
setup:
	go run ./cmd/devstack secrets .local/dev
build:
	docker compose build db api frontend
up:
	docker compose up -d --wait
down:
	docker compose down
logs:
	docker compose logs --tail=100
test:
	go vet ./... && go test ./...
docker-test:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/Test-DockerCore.ps1
format:
	gofmt -w cmd internal web
	npx prettier --write .
fmt: format
