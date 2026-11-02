FROM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS templ
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download && go mod verify
RUN go install github.com/a-h/templ/cmd/templ@latest
COPY backend/internal/web ./internal/web
RUN templ generate --watch=false

FROM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download && go mod verify
COPY --from=templ /src/internal/web ./internal/web
COPY backend/cmd ./cmd
COPY backend/internal/database ./internal/database
COPY backend/internal/config ./internal/config
COPY backend/internal/catalog ./internal/catalog
COPY backend/internal/httpapi ./internal/httpapi
COPY backend/internal/localhttp ./internal/localhttp
COPY backend/internal/migration ./internal/migration
COPY backend/internal/process ./internal/process
COPY backend/internal/provider ./internal/provider
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/ ./cmd/api ./cmd/worker ./cmd/migrate ./cmd/devstack ./cmd/web ./cmd/fakeprovider ./cmd/probe ./cmd/stock

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ /app/
USER 10001:10001
EXPOSE 5001 8080
ENTRYPOINT ["/app/api"]
