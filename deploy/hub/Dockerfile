# kergui-hub — the central node of RFC-005. Pure Go, no cgo, distroless.
# Production: Dokploy builds this file from the repository on a hub-v* tag
# (docs/hub-production.md). Locally: deploy/hub/compose.yaml.
#   docker build -f deploy/hub/Dockerfile --build-arg VERSION=0.3.0 .
# VERSION is what `kergui-hub -version` and GET /health report; without the
# build arg it is "dev". No secret is baked in: everything comes from the
# environment at run time (Dokploy's Environment tab, or hub.env locally).

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /kergui-hub ./cmd/kergui-hub

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager" \
      org.opencontainers.image.title="kergui-hub" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /kergui-hub /kergui-hub
EXPOSE 8090
# The image has no shell or curl: the binary checks its own /ready (the
# database answers). Docker Swarm, which runs Dokploy's apps, replaces a
# container that stays unhealthy and waits for a healthy one on redeploy.
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/kergui-hub", "-healthcheck"]
ENTRYPOINT ["/kergui-hub"]
