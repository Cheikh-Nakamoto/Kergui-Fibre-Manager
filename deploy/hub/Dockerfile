# kergui-hub — the central node of RFC-005. Pure Go, no cgo, distroless.
# Built and pushed to GHCR by hub-ci.yml on a hub-v* tag; run with
# deploy/hub/compose.yaml. `kergui-hub -version` prints the embedded version.
# No secret is baked in: everything comes from the environment (hub.env).

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
ENTRYPOINT ["/kergui-hub"]
