# The demo image: wsl-shrinker's screen on a made-up PC, in any terminal with
# Docker, before anything is installed. Nothing real is read or changed:
#
#   docker run --rm -it ghcr.io/kanggu12/wsl-shrinker-demo
#   docker run --rm -it -e WSL_SHRINKER_LANG=ko ghcr.io/kanggu12/wsl-shrinker-demo
#
# Built from the repository root by .github/workflows/release.yml for every
# release, for linux/amd64 and linux/arm64; `make demo` builds and opens it
# from a working tree. The program is the Linux build the release zips carry,
# which opens the demo by itself on any Linux.
ARG GO_VERSION=1.26
# What `wsl-shrinker version` reports inside the image; the release workflow
# passes the tag. A local build leaves them empty and the program says "dev".
ARG VERSION=
ARG COMMIT=
ARG DATE=

# The build runs on the builder's own platform and cross-compiles for the
# image's, so no emulation is needed for arm64.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
# The modules first, so a change to the code reuses their layer.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETARCH
ARG VERSION
ARG COMMIT
ARG DATE
# Static, as in the release zips: the image needs no C library.
RUN v=github.com/kanggu12/wsl-shrinker/internal/version; \
    CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X $v.Version=$VERSION -X $v.Commit=$COMMIT -X $v.Date=$DATE" \
      -o /out/wsl-shrinker ./cmd/wsl-shrinker

# Nothing but the program and the licenses that cover it.
FROM scratch
COPY --from=build /out/wsl-shrinker /usr/local/bin/wsl-shrinker
COPY LICENSE /usr/share/licenses/wsl-shrinker/LICENSE
COPY docs/THIRD_PARTY_NOTICES.md /usr/share/licenses/wsl-shrinker/THIRD_PARTY_NOTICES.md
# A terminal with colors, UTF-8 for the screen's blocks and lines, and a home
# the program's settings and cache folders resolve under; the demo writes none.
ENV TERM=xterm-256color LANG=C.UTF-8 HOME=/home/demo
# Any user will do: the demo reads nothing of the system.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/wsl-shrinker"]
# Bare, the image opens the demo; given a command (version, --help), it runs
# that instead.
CMD ["--demo"]
