# Compile natively on the build host and cross-compile the plugin for s390x.
# $BUILDPLATFORM is the machine running podman (e.g. linux/arm64 on Apple
# Silicon). Without it, `podman build --platform=linux/s390x` pulls an s390x
# golang image and `RUN` needs QEMU (`exec format error` if qemu-user is
# missing). CGO is off, so the binary does not need an s390x toolchain.
FROM --platform=$BUILDPLATFORM quay.io/projectquay/golang:1.26 AS builder
# The build context carries no .git (see .dockerignore), so the build
# identity comes in from outside: podman build --build-arg VERSION="$(git
# describe --tags --always --dirty --long)". Left at the default, the binary
# reports "dev".
ARG VERSION=dev
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=s390x make VERSION=$VERSION

# Runtime is always IBM Z. Pinned by digest for reproducible image builds.
# The tag is documentation. To bump: resolve the current digest of the wanted
# tag, e.g.
#   skopeo inspect --raw docker://registry.access.redhat.com/ubi10-minimal:latest
FROM --platform=linux/s390x registry.access.redhat.com/ubi10-minimal:10.2-1786960640@sha256:61f820b7893b6226e499e928db99c59a0a9135aa17e4e056fdaf1015908cca14
COPY --from=builder /app/cex-dra-kubeletplugin /cex-dra-kubeletplugin
ENTRYPOINT ["/cex-dra-kubeletplugin"]
