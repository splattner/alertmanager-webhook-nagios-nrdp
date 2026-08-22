# syntax=docker/dockerfile:1

# --platform=$BUILDPLATFORM keeps this stage on the builder's native
# architecture. Without it, buildx runs the whole Go toolchain under QEMU for
# every non-native target, turning a one-minute compile into twenty. GOARCH
# below does the cross-compiling instead, which is free because CGO is off.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build

ARG OCI_VERSION=dev
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Warm the module cache separately so source edits do not refetch dependencies.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# CGO_ENABLED=0 keeps the binary static so it runs on the distroless base.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${OCI_VERSION}" \
    -o /out/nrdp-webhook ./cmd/nrdp-webhook

FROM gcr.io/distroless/static-debian12:nonroot

ARG OCI_VERSION=dev
ARG OCI_REVISION=unknown
ARG OCI_CREATED=unknown

LABEL org.opencontainers.image.title="alertmanager-webhook-nagios-nrdp" \
      org.opencontainers.image.description="Forwards Prometheus Alertmanager alerts to Nagios as passive check results via NRDP" \
      org.opencontainers.image.source="https://github.com/splattner/alertmanager-webhook-nagios-nrdp" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${OCI_VERSION}" \
      org.opencontainers.image.revision="${OCI_REVISION}" \
      org.opencontainers.image.created="${OCI_CREATED}"

COPY --from=build /out/nrdp-webhook /usr/local/bin/nrdp-webhook

# Numeric, not the "nonroot" name the base image declares: kubelet cannot verify
# a non-numeric user against runAsNonRoot and refuses to start the container
# with CreateContainerConfigError. 65532 is distroless's nonroot uid.
USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/nrdp-webhook"]
CMD ["serve"]
