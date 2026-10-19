# kergui-hub — the central node of RFC-005. Pure Go, no cgo, distroless.
# Built on GitHub Actions (hub-ci.yml); run with deploy/hub/compose.yaml.

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
COPY --from=build /kergui-hub /kergui-hub
EXPOSE 8090
ENTRYPOINT ["/kergui-hub"]
