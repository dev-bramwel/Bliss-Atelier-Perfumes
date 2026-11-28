.PHONY: setup build up down logs test docker-test format fmt k8s-up k8s-down k8s-drill k8s-status
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
k8s-up:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/k8s-local-secrets.ps1
	kubectl apply -k infra/k8s/base
k8s-down:
	kubectl delete namespace bliss-local --ignore-not-found
k8s-drill:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/k8s-drill.ps1
k8s-status:
	kubectl get pods,svc,ingress,hpa -n bliss-local
