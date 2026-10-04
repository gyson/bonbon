.PHONY: build release release-assets test check web-build web-check

VERSION ?= dev

build: web-build
	CGO_ENABLED=0 go build -trimpath -o bin/bonbon ./cmd/bonbon

release: web-build
	CGO_ENABLED=0 go build -trimpath -ldflags '-X main.instanceDirName=.bonbon -X main.version=$(VERSION)' -o bin/release/bonbon ./cmd/bonbon

release-assets: web-build
	sh scripts/package-release.sh '$(VERSION)'

test: web-build
	CGO_ENABLED=0 go test ./...

check: web-build
	go vet ./...
	sh -n internal/install/install.sh scripts/package-release.sh scripts/smoke-release.sh

web-build:
	npm --prefix web run build

web-check:
	npm --prefix web test
