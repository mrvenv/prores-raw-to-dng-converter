# VERSION holds the version of the next release; see CONTRIBUTING.md, "Releasing".
VERSION     := $(shell cat VERSION)
VERSION_VAR := rawconv/internal/version.Version
# Local builds are marked so they are never mistaken for a release.
LDFLAGS     := -X=$(VERSION_VAR)=$(VERSION)-dev
FFMPEG_DIR  := third_party/ffmpeg
DIST_DIR    := dist

.PHONY: build gui cli test ffmpeg dist clean

build: gui cli

gui:
	go build -ldflags "$(LDFLAGS)" -o bin/rawconv .

cli:
	go build -ldflags "$(LDFLAGS)" -o bin/rawconv-cli ./cmd/rawconv-cli
	go build -ldflags "$(LDFLAGS)" -o bin/prrinfo ./cmd/prrinfo

test:
	go vet ./...
	go test ./...

# Static LGPL ffmpeg/ffprobe with only what rawconv needs, into $(FFMPEG_DIR).
ffmpeg:
	scripts/build-ffmpeg.sh $(FFMPEG_DIR)

# Release archive for this platform, with ffmpeg bundled, into $(DIST_DIR)
# (needs `make ffmpeg` and: go install fyne.io/tools/cmd/fyne@latest).
dist:
	scripts/package.sh $(VERSION)-dev $(FFMPEG_DIR) $(DIST_DIR)

clean:
	rm -rf bin $(DIST_DIR)
