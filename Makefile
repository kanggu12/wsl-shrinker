# Everyday tasks. CI runs `make check`.

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
VERSION ?= $(shell awk '/^## \[[0-9]/ { gsub(/[][]/, "", $$2); print $$2; exit }' CHANGELOG.md)

.PHONY: build check fmt vet lint test vuln fuzz test-windows dist demo notices badges clean

## build: the Windows program and its WSL launcher, in dist/
build:
	CGO_ENABLED=0 GOOS=windows go build -trimpath -o dist/wsl-shrinker.exe ./cmd/wsl-shrinker
	CGO_ENABLED=0 GOOS=linux go build -trimpath -o dist/wsl-shrinker ./cmd/wsl-shrinker

## check: everything CI checks
check: fmt vet lint test

fmt:
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "not gofmt'ed:"; echo "$$out"; exit 1; fi

# The Windows code is vetted too: it builds only there.
# Only the project's packages: work tools in an ignored scratch/ stay out.
PKGS := ./cmd/... ./internal/...

vet:
	go vet $(PKGS)
	GOOS=windows go vet $(PKGS)

# staticcheck is built for this machine once, then run for both systems:
# `go run` with GOOS=windows would build it as a Windows program.
lint:
	go install $(STATICCHECK)
	$$(go env GOPATH)/bin/staticcheck $(PKGS)
	GOOS=windows $$(go env GOPATH)/bin/staticcheck $(PKGS)
	shellcheck scripts/*.sh

test:
	go test -race -count=1 $(PKGS)

## vuln: known vulnerabilities in the code paths the program reaches, for Linux and Windows
# Built for this machine once, as staticcheck is, then run for both systems.
vuln:
	go install $(GOVULNCHECK)
	$$(go env GOPATH)/bin/govulncheck $(PKGS)
	GOOS=windows $$(go env GOPATH)/bin/govulncheck $(PKGS)

## fuzz: every fuzz target for FUZZTIME each (default 30s); failures land in testdata/fuzz
FUZZTIME ?= 30s
fuzz:
	@sh scripts/fuzz.sh $(FUZZTIME)

## test-windows: from WSL, the tests again as Windows programs (the Windows API code)
test-windows:
	scripts/test-windows.sh

## dist: the release zips and checksums for VERSION (default: the newest in CHANGELOG.md)
dist:
	scripts/dist.sh $(VERSION)

## demo: the demo image from this work tree, opened in this terminal (needs Docker)
demo:
	docker build -f docker/demo.Dockerfile -t wsl-shrinker-demo .
	docker run --rm -it wsl-shrinker-demo

## notices: docs/THIRD_PARTY_NOTICES.md from what the program is built from
notices:
	scripts/third-party-notices.sh

## badges: the README's badges in docs/assets/badges, for a release
badges:
	scripts/badges.sh

# A dist run that failed halfway leaves its staging folders behind.
clean:
	rm -f dist/*.zip dist/checksums.txt dist/install.ps1 dist/wsl-shrinker.json dist/wsl-shrinker.exe dist/wsl-shrinker
	rm -rf dist/stage-amd64 dist/stage-arm64
