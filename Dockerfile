# Kergui Fibre Manager — static image for `docker compose up -d` (see compose.yaml).
# Pure Go, no cgo: the binary runs on distroless/static, which still carries the
# CA certificates (NabooPay, Cloudflare) and tzdata.

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /kergui ./cmd/kergui
# Ports 53/80/443 without root: the container runs as the host user that owns ./data.
RUN apt-get update && apt-get install -y --no-install-recommends libcap2-bin \
    && setcap cap_net_bind_service=+ep /kergui

FROM gcr.io/distroless/static-debian12
COPY --from=build /kergui /kergui
WORKDIR /data
ENTRYPOINT ["/kergui"]
CMD ["serve"]
