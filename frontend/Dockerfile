FROM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
COPY backend/cmd/web ./cmd/web
COPY backend/cmd/probe ./cmd/probe
COPY backend/internal/localhttp ./internal/localhttp
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/ ./cmd/web ./cmd/probe

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ /app/
COPY frontend/*.html frontend/*.css frontend/*.js /site/
COPY frontend/img /site/img/
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/web"]
