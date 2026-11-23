# Karakuri build system.

# Build identity stamped into internal/version. VERSION is the single source of
# truth for the release number; commit and date describe the build itself.
GO_MODULE   := github.com/grmrgecko/karakuri
VERSION_PKG := $(GO_MODULE)/internal/version
VERSION ?= $(shell cat VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
BUILD   := build

# The development server runs from the checkout under the user's systemd
# manager, so it remains available for review after the build shell exits.
SYSTEMD_USER_DIR      ?= $(HOME)/.config/systemd/user
DEV_SERVICE_TEMPLATE := systemd/karakuri-dev.service.in
DEV_SERVICE          := $(SYSTEMD_USER_DIR)/karakuri-dev.service

# Build mode: production (default) or development.
MODE ?= production

# Target platform for the portable binaries, defaulting to this host. A cross
# build lands under its own name so an artifact for another machine cannot sit
# where the host build goes and leave `make` thinking it is up to date.
HOST_GOOS   := $(shell go env GOHOSTOS)
HOST_GOARCH := $(shell go env GOHOSTARCH)
GOOS        ?= $(HOST_GOOS)
GOARCH      ?= $(HOST_GOARCH)
ifeq ($(GOOS)/$(GOARCH),$(HOST_GOOS)/$(HOST_GOARCH))
AGENT_BIN := $(BUILD)/karakuri-agent
else
AGENT_BIN := $(BUILD)/karakuri-agent-$(GOOS)-$(GOARCH)$(if $(filter windows,$(GOOS)),.exe,)
endif
ifeq ($(GOOS)/$(GOARCH),$(HOST_GOOS)/$(HOST_GOARCH))
AUTO_BIN := $(BUILD)/karakuri-auto
else
AUTO_BIN := $(BUILD)/karakuri-auto-$(GOOS)-$(GOARCH)$(if $(filter windows,$(GOOS)),.exe,)
endif

# CPython WebAssembly module powering the sandboxed Python script node. It is a
# 26 MB download kept beside the binary rather than embedded in it. Every build,
# release archive, and image carries it; PYTHON_SCRIPTS=0 leaves it out, and
# JavaScript and Bash scripts work either way.
PYTHON_SCRIPTS      ?= 1
PYTHON_WASM_VERSION ?= 3.12.0+20231211-040d5a6
PYTHON_WASM_URL     ?= https://github.com/vmware-labs/webassembly-language-runtimes/releases/download/python%2F$(PYTHON_WASM_VERSION)/python-3.12.0.wasm
PYTHON_WASM         := $(BUILD)/runtimes/python.wasm
RUNTIME_DEPS        := $(if $(filter 0,$(PYTHON_SCRIPTS)),,$(PYTHON_WASM))

ifeq ($(MODE),development)
VITE_FLAGS  := -- --mode development
CONFIG_LINK := config.dev.yaml
else
VITE_FLAGS  :=
CONFIG_LINK := config.prod.yaml
endif

# Go flags.
GO_LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)

# Source file sets. docs/ is in the list because its markdown is embedded in the
# binary: editing a reference page has to rebuild, or the server keeps serving
# the page it was compiled with.
SERVER_GO_SRC := Makefile VERSION go.mod go.sum \
                 $(shell find cmd/karakuri internal pkg docs -name '*.go' -type f 2>/dev/null) \
                 $(shell find docs -name '*.md' -type f 2>/dev/null)
AGENT_GO_SRC  := Makefile VERSION go.mod go.sum automation/go.mod automation/go.sum \
                 $(shell find cmd/karakuri-agent agent internal automation -name '*.go' -type f 2>/dev/null)
AUTO_GO_SRC   := Makefile VERSION go.mod go.sum automation/go.mod automation/go.sum \
                 automation/unlockprovider/karakuri-unlock.dll \
                 $(shell find cmd/karakuri-auto internal/version automation -name '*.go' -type f 2>/dev/null)
FRONTEND_SRC := $(shell find frontend/src frontend/public -type f 2>/dev/null) \
                $(shell find frontend -maxdepth 1 -type f ! -name 'README.md' 2>/dev/null) Makefile

# Build stamps.
MODE_FILE      := $(BUILD)/.mode
# The frontend builds into pkg/http/dist, which the server binary embeds.
FRONTEND_DIST  := pkg/http/dist
FRONTEND_STAMP := $(BUILD)/.frontend-stamp
CONFIG_STAMP   := $(BUILD)/etc/.stamp

# Plugin packages. A plugin directory holds a manifest and may contribute a Go
# executable, dashboard widget packages, or both, so the two are discovered
# separately: PLUGIN_BINS from main.go, PLUGIN_WIDGETS from each widget's
# package.json. Widget paths carry the plugin/package pair as plugin:package.
PLUGIN_DIRS    := $(patsubst plugins/%/manifest.yaml,%,$(wildcard plugins/*/manifest.yaml))
PLUGIN_BINS    := $(patsubst plugins/%/main.go,%,$(wildcard plugins/*/main.go))
PLUGIN_WIDGETS := $(subst /widgets/,:,$(patsubst plugins/%/package.json,%,$(wildcard plugins/*/widgets/*/package.json)))
WIDGET_STAMPS  := $(foreach w,$(PLUGIN_WIDGETS),$(BUILD)/plugins/$(subst :,/widgets/,$(w))/.stamp)

# Themes are static directories copied into build/themes/<name>/.
THEME_DIRS    := $(patsubst themes/%/,%,$(filter %/,$(wildcard themes/*/)))
THEME_STAMPS  := $(foreach t,$(THEME_DIRS),$(BUILD)/themes/$(t)/.stamp)

# ── Default target (production) ───────────────

.PHONY: all
all: $(BUILD)/karakuri $(AGENT_BIN) $(AUTO_BIN) $(FRONTEND_STAMP) $(CONFIG_STAMP) plugins themes $(RUNTIME_DEPS)
	@mkdir -p $(BUILD)/var
	@printf "==> Build complete: $(BUILD)/karakuri, $(AGENT_BIN), $(AUTO_BIN)\n"

# ── Mode tracking ─────────────────────────────
# Rebuilds frontend and config when the mode changes.

$(MODE_FILE): FORCE
	@mkdir -p $(BUILD)
	@echo "$(MODE)" | cmp -s - $@ || echo "$(MODE)" > $@

.PHONY: FORCE
FORCE:

# ── Development target ────────────────────────

.PHONY: dev dev-all dev-server dev-frontend dev-agent dev-auto dev-service
dev:
	@case "$(TARGET)" in \
		"") target=dev-all ;; \
		server|frontend|agent|auto) target=dev-$(TARGET) ;; \
		*) printf "Error: TARGET must be server, frontend, agent, or auto\n" >&2; exit 1 ;; \
	esac; \
	$(MAKE) --no-print-directory MODE=development $$target

dev-all: all dev-service
dev-server: dev-service
dev-frontend: $(BUILD)/karakuri
dev-agent: $(AGENT_BIN)
dev-auto: $(AUTO_BIN)

dev-service: $(BUILD)/karakuri $(CONFIG_STAMP) $(DEV_SERVICE_TEMPLATE)
	@if [ "$(HOST_GOOS)" != "linux" ] || ! command -v systemctl >/dev/null 2>&1; then \
		printf "==> systemd user services are unavailable; skipping karakuri-dev.service\n"; \
		exit 0; \
	fi; \
	mkdir -p "$(SYSTEMD_USER_DIR)"; \
	tmp="$(DEV_SERVICE).tmp"; \
	sed 's|@ROOT@|$(CURDIR)|g' "$(DEV_SERVICE_TEMPLATE)" > "$$tmp"; \
	if cmp -s "$$tmp" "$(DEV_SERVICE)"; then \
		rm -f "$$tmp"; \
		printf "==> Development service is current: $(DEV_SERVICE)\n"; \
	else \
		mv "$$tmp" "$(DEV_SERVICE)"; \
		systemctl --user daemon-reload; \
		printf "==> Installed development service: $(DEV_SERVICE)\n"; \
	fi

# ── Go binary ─────────────────────────────────

# CGO_ENABLED=0 is pinned for the same reason it is on the agent: the SQLite
# driver is pure Go (wasm under wazero), so a dependency that grows a cgo path
# fails the build here rather than quietly tying the server to a C toolchain.
# The binary embeds the frontend bundle, so a frontend change rebuilds it.
$(BUILD)/karakuri: $(SERVER_GO_SRC) $(FRONTEND_STAMP)
	@printf "==> Building Go binary...\n"
	@mkdir -p $(BUILD)
	CGO_ENABLED=0 go build -ldflags "$(GO_LDFLAGS)" -o $@ ./cmd/karakuri

# The agent is a separate binary meant to run on remote hosts, built by default
# so the two are never a release apart. It links no cgo and no database, so one
# machine cross-compiles every artifact: set GOOS and GOARCH for macOS, Linux,
# FreeBSD, or Windows. CGO_ENABLED=0 is pinned so a dependency that grows a cgo
# path fails the build here rather than silently tying the agent to the build
# host's toolchain.
# MACOS_SIGN_IDENTITY, when set, signs a darwin agent build with that
# code-signing identity (a Developer ID, or a stable self-signed one). The
# macOS Accessibility grant the GUI nodes need is keyed to the binary's
# signature, so signing every build with one identity keeps the grant across
# updates. Signing runs only for a darwin target and only where codesign
# exists, so a cross build from another host still produces the binary — the
# agent signs itself at `service install` there instead.
MACOS_SIGN_IDENTITY ?=

.PHONY: agent
agent: $(AGENT_BIN)
$(AGENT_BIN): $(AGENT_GO_SRC)
	@printf "==> Building agent binary ($(GOOS)/$(GOARCH))...\n"
	@mkdir -p $(BUILD)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(GO_LDFLAGS)" -o $@ ./cmd/karakuri-agent
	@if [ "$(GOOS)" = "darwin" ] && [ -n "$(MACOS_SIGN_IDENTITY)" ] && command -v codesign >/dev/null 2>&1; then \
		printf "==> Signing $@ with %s\n" "$(MACOS_SIGN_IDENTITY)"; \
		codesign --force --identifier karakuri-agent -s "$(MACOS_SIGN_IDENTITY)" $@; \
	fi

# karakuri-auto exposes the automation module as a shell command. It shares the
# agent's pure-Go platform support but no agent configuration or enrollment
# state, so it is built and distributed as its own binary.
.PHONY: auto
auto: $(AUTO_BIN)
$(AUTO_BIN): $(AUTO_GO_SRC)
	@printf "==> Building automation binary ($(GOOS)/$(GOARCH))...\n"
	@mkdir -p $(BUILD)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(GO_LDFLAGS)" -o $@ ./cmd/karakuri-auto
	@if [ "$(GOOS)" = "darwin" ] && [ -n "$(MACOS_SIGN_IDENTITY)" ] && command -v codesign >/dev/null 2>&1; then \
		printf "==> Signing $@ with %s\n" "$(MACOS_SIGN_IDENTITY)"; \
		codesign --force --identifier org.karakuri.auto -s "$(MACOS_SIGN_IDENTITY)" $@; \
	fi

# The Windows credential provider is the COM DLL the logon screen loads to
# submit the credentials the agent queues for unlock and sign-in. It is C++
# over the Win32 credential-provider API, cross-built with mingw-w64 when
# that toolchain is installed; a host without the compiler skips the target
# rather than failing the build. The built copy is what the agent embeds —
# the target refreshes the committed one under automation/unlockprovider/
# (a build input: go:embed picks it up on the next agent build) and drops a
# copy in build/ for a standalone install. A host without mingw keeps using
# the committed copy, so `make agent` works everywhere.
.PHONY: unlockprovider
unlockprovider:
	@if command -v x86_64-w64-mingw32-g++ >/dev/null 2>&1; then \
		printf "==> Building the Windows credential provider...\n"; \
		mkdir -p $(BUILD); \
		x86_64-w64-mingw32-g++ -shared -static -o automation/unlockprovider/karakuri-unlock.dll \
			automation/unlockprovider/karakuri-unlock.cpp \
			-lole32 -luuid -ladvapi32 -lsecur32 -lwtsapi32 -fno-exceptions -fno-rtti -O2; \
		x86_64-w64-mingw32-strip automation/unlockprovider/karakuri-unlock.dll; \
		cp automation/unlockprovider/karakuri-unlock.dll $(BUILD)/karakuri-unlock.dll; \
	else \
		printf "==> mingw-w64 not installed; keeping the committed credential provider\n"; \
	fi

# ── Plugins ───────────────────────────────────

# Every plugins/<name>/ builds into build/plugins/<name>/ with its manifest
# alongside, where the server's default plugins path finds it: the Go
# executable when the plugin has a main.go, and every widget package it ships
# under widgets/<package>/. go build is incremental, so rebuilding the
# executables every time costs nothing when clean; widgets carry stamps because
# an npm build does not.
.PHONY: plugins
plugins: plugin-bins $(WIDGET_STAMPS)

.PHONY: plugin-bins
plugin-bins:
	@for p in $(PLUGIN_DIRS); do \
	  mkdir -p $(BUILD)/plugins/$$p; \
	  cp plugins/$$p/manifest.yaml $(BUILD)/plugins/$$p/manifest.yaml; \
	done
	@for p in $(PLUGIN_BINS); do \
	  printf "==> Building plugin $$p...\n"; \
	  go build -ldflags "$(GO_LDFLAGS)" -o $(BUILD)/plugins/$$p/$$p ./plugins/$$p || exit 1; \
	done

# ── Frontend ──────────────────────────────────

$(FRONTEND_STAMP): $(FRONTEND_SRC) $(MODE_FILE)
	@printf "==> Building frontend...\n"
	@cd frontend && npm install --silent && npm run build $(VITE_FLAGS)
	@# Earlier builds copied the bundle to build/frontend, which nothing reads now.
	@rm -rf $(BUILD)/frontend
	@touch $@

# ── Dashboard widgets and themes ──────────────

.PHONY: widgets themes
widgets: $(WIDGET_STAMPS)
themes: $(THEME_STAMPS)

# widget_rule builds one plugin-supplied widget package when its sources
# change. $(1) is plugin:package. Prerequisites exclude node_modules, dist, and
# .vite so only real edits trigger rebuilds, and include the plugin's
# widgets/shared/ directory: it holds source two packages import (it has no
# package.json, so it is never built as a package of its own) and editing it
# has to rebuild each of them.
define widget_rule
$(BUILD)/plugins/$(subst :,/widgets/,$(1))/.stamp: $(shell find plugins/$(subst :,/widgets/,$(1)) \
        plugins/$(firstword $(subst :, ,$(1)))/widgets/shared -type f \
        -not -path '*/node_modules/*' -not -path '*/dist/*' -not -path '*/.vite/*' 2>/dev/null) $(MODE_FILE)
	@printf "==> Building widget $(1)...\n"
	@cd plugins/$(subst :,/widgets/,$(1)) && npm install --silent && npm run build $(VITE_FLAGS)
	@rm -rf $(BUILD)/plugins/$(subst :,/widgets/,$(1))
	@mkdir -p $(BUILD)/plugins/$(subst :,/widgets/,$(1))
	@cp -R plugins/$(subst :,/widgets/,$(1))/dist/. $(BUILD)/plugins/$(subst :,/widgets/,$(1))/
	@touch $$@
endef
$(foreach w,$(PLUGIN_WIDGETS),$(eval $(call widget_rule,$(w))))

# theme_rule copies one theme package, a plain directory of manifest and CSS.
define theme_rule
$(BUILD)/themes/$(1)/.stamp: $(wildcard themes/$(1)/*)
	@printf "==> Installing theme $(1)...\n"
	@mkdir -p $(BUILD)/themes/$(1)
	@cp -r themes/$(1)/* $(BUILD)/themes/$(1)/
	@touch $$@
endef
$(foreach t,$(THEME_DIRS),$(eval $(call theme_rule,$(t))))

# ── Script runtimes ───────────────────────────

# Downloads the CPython WebAssembly module so the script node can run Python.
# `all` and the release staging depend on it unless PYTHON_SCRIPTS=0, and the
# file is kept across builds so it downloads once. The checksum published alongside the release is verified when sha256sum
# is available, so a truncated or substituted download fails loudly.
.PHONY: runtimes
runtimes: $(PYTHON_WASM)

$(PYTHON_WASM):
	@printf "==> Downloading CPython WebAssembly runtime ($(PYTHON_WASM_VERSION))...\n"
	@mkdir -p $(BUILD)/runtimes
	@curl -fsSL --retry 3 -o $@.tmp "$(PYTHON_WASM_URL)"
	@if command -v sha256sum >/dev/null 2>&1; then \
		curl -fsSL --retry 3 -o $@.sha256 "$(PYTHON_WASM_URL).sha256sum" || true; \
		if [ -s $@.sha256 ]; then \
			expected=$$(awk '{print $$1}' $@.sha256); \
			actual=$$(sha256sum $@.tmp | awk '{print $$1}'); \
			if [ "$$expected" != "$$actual" ]; then \
				printf "    checksum mismatch: expected $$expected, got $$actual\n" >&2; \
				rm -f $@.tmp $@.sha256; exit 1; \
			fi; \
			printf "    checksum verified\n"; \
		fi; \
		rm -f $@.sha256; \
	fi
	@mv $@.tmp $@
	@printf "==> Python runtime installed: $@\n"

# ── Config generation ─────────────────────────

# Configs are generated once and then left alone. Regenerating mints a fresh
# security.encryption_key, which orphans every secret already encrypted in the
# database (connection credentials become undecryptable). Delete a file to have
# it regenerated, or run `make clean`.
$(CONFIG_STAMP): $(BUILD)/karakuri $(MODE_FILE)
	@printf "==> Checking configurations...\n"
	@mkdir -p $(BUILD)/etc
	@for env in prod dev; do \
		if [ -f $(BUILD)/etc/config.$$env.yaml ]; then \
			printf "    config.$$env.yaml exists, keeping its secrets\n"; \
		else \
			$(BUILD)/karakuri config gen --env=$$env > $(BUILD)/etc/config.$$env.yaml; \
			printf "    generated config.$$env.yaml\n"; \
		fi; \
	done
	@rm -f $(BUILD)/etc/config.yaml
	@ln -s $(CONFIG_LINK) $(BUILD)/etc/config.yaml
	@touch $@

# ── Clean ─────────────────────────────────────

.PHONY: clean
clean:
	@printf "==> Cleaning build artifacts...\n"
	rm -rf $(BUILD)/frontend $(BUILD)/etc $(BUILD)/var $(BUILD)/karakuri $(BUILD)/karakuri-agent* $(BUILD)/karakuri-auto* $(BUILD)/.mode $(FRONTEND_STAMP)
	rm -rf $(BUILD)/runtimes $(BUILD)/plugins $(BUILD)/themes $(RELEASE_DIR) $(RELEASE_CONFIG)
	rm -rf $(DOCKER_CONTEXT) dist
	rm -rf frontend/dist frontend/node_modules
	find $(FRONTEND_DIST) -mindepth 1 ! -name .gitkeep -delete
	rm -rf plugins/*/widgets/*/dist plugins/*/widgets/*/node_modules
	@printf "==> Clean complete.\n"

# ── Install (requires root) ──────────────────

# install.sh is the installer, and it ships in the release archive: the same
# script an operator runs after unpacking is the one that runs here, so a
# system install cannot behave one way from a build tree and another from a
# release. It installs files only; `karakuri service install` registers the
# service afterwards. build/ already has the layout it expects, and there is no `all`
# prerequisite because this target runs as root: a rebuild here would leave
# node_modules and the Go cache owned by root.
.PHONY: install
install:
	./install.sh --src $(BUILD)

# ── Docker ────────────────────────────────────

# Two images: the server (DOCKER_IMAGE) and the remote agent (DOCKER_AGENT_IMAGE).
# Each builds two ways. `make docker` and `make docker-agent` build from this
# checkout, which is what a clone can do in one command. `make docker-release`
# and `make docker-agent-release` assemble the image from the archives a snapshot
# or release already produced, compiling nothing, so a published image ships the
# same bytes the archives do rather than a rebuild that ought to match.
DOCKER_IMAGE       ?= grmrgecko/karakuri
DOCKER_AGENT_IMAGE ?= grmrgecko/karakuri-agent
DOCKER_CONTEXT     := $(BUILD)/docker
# Platforms the release images are built for. Each needs an unpacked archive
# in dist/, and building them needs docker buildx, the containerd image store
# to load more than one, and binfmt emulation for the architectures that are
# not the host's, since the images install packages. The checkout builds are
# for the host, where a quick local image is what they are for.
DOCKER_PLATFORMS   ?= linux/amd64,linux/arm64
# The headless Chromium and fonts a dashboard render needs are most of the
# server image; a deployment that never renders builds with DASHBOARD_RENDERS=0.
DASHBOARD_RENDERS  ?= 1

# website renders the guides in docs/ into website/docs.html with the template
# in tools/website-docs. It is not part of the default build; run it before
# publishing website/.
.PHONY: website
website:
	go run ./tools/website-docs -template tools/website-docs/docs.html.tmpl -out website/docs.html

# website-og renders the link preview card in tools/website-og/card.html into
# website/assets/og-image.png with headless Chromium. Rerun it after changing
# the card or the mascot.
.PHONY: website-og
website-og:
	chromium --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
		--window-size=1200,630 --virtual-time-budget=5000 \
		--screenshot=website/assets/og-image.png "file://$(CURDIR)/tools/website-og/card.html"

.PHONY: docker
docker:
	docker build -t karakuri:$(VERSION) --build-arg VERSION=$(VERSION) \
		--build-arg DASHBOARD_RENDERS=$(DASHBOARD_RENDERS) --build-arg PYTHON_SCRIPTS=$(PYTHON_SCRIPTS) .

.PHONY: docker-agent
docker-agent:
	docker build -f docker/Dockerfile.agent -t karakuri-agent:$(VERSION) \
		--build-arg VERSION=$(VERSION) .

# The release builds unpack each Linux archive into build/docker/<image>/<arch>/
# and tag the image from the archive name rather than the VERSION file, so a
# snapshot image is labelled as one instead of claiming to be the release it was
# built before.
RELEASE_IMAGE = IMAGE=$(1) PLATFORMS=$(DOCKER_PLATFORMS) HOST_ARCH=$(HOST_GOARCH) docker/release-image.sh

.PHONY: docker-release
docker-release:
	@NAME=karakuri DOCKERFILE=docker/Dockerfile.release CONTEXT=$(DOCKER_CONTEXT)/server \
		FILES="docker/entrypoint.sh" BUILD_ARGS="DASHBOARD_RENDERS=$(DASHBOARD_RENDERS)" ENSURE_DIRS=runtimes \
		$(call RELEASE_IMAGE,$(DOCKER_IMAGE)) build

.PHONY: docker-agent-release
docker-agent-release:
	@NAME=karakuri-agent DOCKERFILE=docker/Dockerfile.agent-release CONTEXT=$(DOCKER_CONTEXT)/agent \
		FILES="docker/agent-entrypoint.sh docker/agent.yaml" \
		$(call RELEASE_IMAGE,$(DOCKER_AGENT_IMAGE)) build

# Pushes what the release builds made. Separate because publishing is a
# decision: the daemon has to be logged in to the registry, and a release does
# not push images on its own.
.PHONY: docker-push
docker-push:
	@$(call RELEASE_IMAGE,$(DOCKER_IMAGE)) push

.PHONY: docker-agent-push
docker-agent-push:
	@$(call RELEASE_IMAGE,$(DOCKER_AGENT_IMAGE)) push

# ── Release ───────────────────────────────────

# A release is cross-compiled inside a container. The server is pure Go now
# (SQLite runs as wasm under wazero), but the MIDI plugin links C++ over ALSA,
# so the host toolchain reaches only the host's own platform for that one
# artifact; docker/Dockerfile.cross adds the ALSA headers to goreleaser-cross,
# which carries the Linux, macOS, and Windows compilers. The
# web assets are built on the host first, because that image has no node, and
# staged into build/release/ where .goreleaser.yaml picks them up.
GO_CROSS_VERSION ?= v1.26.5
CROSS_IMAGE      ?= karakuri-cross:$(GO_CROSS_VERSION)
RELEASE_DIR      := $(BUILD)/release
RELEASE_ENV      ?= .release-env
CACHE            := $(CURDIR)/.cache

# Release builds are sized to the memory free when they start. goreleaser runs
# one build per CPU by default and each go build compiles one package per CPU,
# so on a many-core host the two multiply into more linkers and compilers than
# memory holds, and the kernel's OOM killer picks processes outside the build.
# RELEASE_JOB_MB is the memory one build is budgeted: six builds at once took
# about 15 GB, 2.5 GB each, so 4 GB leaves headroom. RELEASE_PARALLELISM is
# how many builds run at once, the free memory over that budget, at least one
# and at most the CPU count; RELEASE_GO_PROCS splits the CPUs between them so
# the total stays at the CPU count. The container is capped at three quarters
# of the free memory, so a build that still outgrows it fails inside the
# container instead of taking other processes down.
RELEASE_JOB_MB      ?= 4096
HOST_CPUS           := $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)
HOST_MEM_FREE_MB    := $(shell awk '/^MemAvailable:/ { print int($$2 / 1024) }' /proc/meminfo 2>/dev/null)
RELEASE_PARALLELISM ?= $(shell n=$$(( $(or $(HOST_MEM_FREE_MB),$(RELEASE_JOB_MB)) / $(RELEASE_JOB_MB) )); \
	[ $$n -lt 1 ] && n=1; [ $$n -gt $(HOST_CPUS) ] && n=$(HOST_CPUS); echo $$n)
RELEASE_GO_PROCS    ?= $(shell n=$$(( $(HOST_CPUS) / $(RELEASE_PARALLELISM) )); [ $$n -lt 1 ] && n=1; echo $$n)
RELEASE_MEMORY      ?= $(if $(HOST_MEM_FREE_MB),--memory=$(shell echo $$(( $(HOST_MEM_FREE_MB) * 3 / 4 )))m)
GORELEASER_FLAGS     = --parallelism $(RELEASE_PARALLELISM)
# macOS binaries are signed and notarized with the credentials in .signing/
# when it holds them (docker/macos-sign.sh); MACOS_NOTARIZE=0 signs only.
MACOS_NOTARIZE      ?= 1

# goreleaser runs as the invoking user so dist/ is not left owned by root, and
# the module and build caches live under .cache/ so a second run is not a second
# download. KARAKURI_VERSION is what a snapshot names itself after, keeping the
# VERSION file the only place the release number is written, and KARAKURI_COMMIT
# is what stamps the build until the repository has a remote for goreleaser to
# read the commit from.
GORELEASER_RUN = docker run --rm \
	--user $$(id -u):$$(id -g) \
	$(RELEASE_MEMORY) \
	-e GOFLAGS=-p=$(RELEASE_GO_PROCS) \
	-e MACOS_NOTARIZE=$(MACOS_NOTARIZE) \
	-v $(CURDIR):/go/src/$(GO_MODULE) \
	-v $(CACHE):/.cache \
	-v $(CACHE)/go:/go \
	-w /go/src/$(GO_MODULE) \
	-e KARAKURI_VERSION=$(VERSION) \
	-e KARAKURI_COMMIT=$(COMMIT)

# Builds the cross-compilation image. Docker layer caching makes a repeat run
# cheap, so snapshot and release both depend on it rather than asking the
# operator to remember.
.PHONY: cross-image
cross-image:
	@printf "==> Building cross-compilation image $(CROSS_IMAGE)...\n"
	@docker build --quiet -f docker/Dockerfile.cross \
	    --build-arg GO_CROSS_VERSION=$(GO_CROSS_VERSION) \
	    -t $(CROSS_IMAGE) . >/dev/null

# Stages what the archives carry beside the binaries: each plugin's manifest
# and built widget packages, the themes, and the CPython module unless
# PYTHON_SCRIPTS=0. Everything is copied from the
# package's own dist/ rather than from build/, and into an emptied directory, so
# a stale chunk from an earlier build cannot ride along. The frontend is built
# here too but not staged: goreleaser compiles it into the server binary from
# pkg/http/dist.
.PHONY: release-assets
release-assets: $(FRONTEND_STAMP) $(WIDGET_STAMPS) $(THEME_STAMPS) $(RUNTIME_DEPS)
	@printf "==> Staging release assets...\n"
	@rm -rf $(RELEASE_DIR)
	@mkdir -p $(RELEASE_DIR)
	@if [ -n "$(RUNTIME_DEPS)" ]; then \
	  mkdir -p $(RELEASE_DIR)/runtimes; \
	  cp $(PYTHON_WASM) $(RELEASE_DIR)/runtimes/; \
	else \
	  printf "    PYTHON_SCRIPTS=0, so the archives carry no Python runtime\n"; \
	fi
	@for p in $(PLUGIN_DIRS); do \
	  mkdir -p $(RELEASE_DIR)/plugins/$$p; \
	  cp plugins/$$p/manifest.yaml $(RELEASE_DIR)/plugins/$$p/manifest.yaml; \
	done
	@for w in $(PLUGIN_WIDGETS); do \
	  d=$$(echo $$w | sed 's|:|/widgets/|'); \
	  mkdir -p $(RELEASE_DIR)/plugins/$$d; \
	  cp -R plugins/$$d/dist/. $(RELEASE_DIR)/plugins/$$d/; \
	done
	@for t in $(THEME_DIRS); do \
	  mkdir -p $(RELEASE_DIR)/themes/$$t; \
	  cp -R themes/$$t/. $(RELEASE_DIR)/themes/$$t/; \
	done

# The config goreleaser actually runs. .goreleaser.yaml is the source; the
# plugin entries are generated from the directories that exist, so adding a
# plugin is adding a directory.
RELEASE_CONFIG := $(BUILD)/goreleaser.yaml

# Which plugins need cgo, asked of the compiler rather than guessed from the
# import graph: a dependency that looks platform-gated and one that is really a
# C library are the same shape from the outside, and guessing wrong the
# forgiving way drops a plugin from the macOS archive without a word. Recursive
# so the probe only runs when a release config is being written.
PLUGIN_CGO = $(shell for p in $(PLUGIN_BINS); do \
	  CGO_ENABLED=0 go build -o /dev/null ./plugins/$$p >/dev/null 2>&1 || echo $$p; \
	done)

# Expands the @plugin-builds@ and @plugin-ids@ markers. A cgo plugin is built
# for Linux alone, because that is the platform the cross image can be given
# development headers for; everything else follows the server, whose lists it
# borrows through YAML anchors so the two cannot drift. The C++ runtime is
# linked in statically because the cross image's is newer than the systems the
# artifacts run on: a plugin built against it failed to start on Debian stable
# with a GLIBCXX version error, in the container image and on any host older
# than the builder. What stays dynamic is the C library and the one the plugin
# is actually for.
.PHONY: release-config
release-config:
	@printf "==> Generating release config...\n"
	@mkdir -p $(BUILD)
	@cgo="$(PLUGIN_CGO)"; \
	cgo_ldflags='CGO_LDFLAGS=-static-libstdc++ -static-libgcc'; \
	while IFS= read -r line; do \
	  case "$$line" in \
	    *@plugin-builds@*) \
	      for p in $(PLUGIN_BINS); do \
	        printf '  - id: plugin-%s\n    main: ./plugins/%s\n    binary: plugins/%s/%s\n' "$$p" "$$p" "$$p" "$$p"; \
	        printf '    mod_timestamp: "{{ .CommitTimestamp }}"\n    flags: [-trimpath]\n    ldflags: *ldflags\n    hooks: *macos_sign\n'; \
	        case " $$cgo " in \
	          *" $$p "*) \
	            printf '    env: [CGO_ENABLED=1, "%s"]\n    goos: [linux]\n    goarch: *server_goarch\n' "$$cgo_ldflags"; \
	            printf '    overrides:\n      - goos: linux\n        goarch: arm64\n'; \
	            printf '        env: [CGO_ENABLED=1, "%s", CC=aarch64-linux-gnu-gcc, CXX=aarch64-linux-gnu-g++]\n' "$$cgo_ldflags" ;; \
	          *) \
	            printf '    env: [CGO_ENABLED=0]\n    goos: *server_goos\n    goarch: *server_goarch\n    ignore: *server_ignore\n' ;; \
	        esac; \
	        printf '\n'; \
	      done ;; \
	    *@plugin-ids@*) \
	      for p in $(PLUGIN_BINS); do printf '      - plugin-%s\n' "$$p"; done ;; \
	    *) printf '%s\n' "$$line" ;; \
	  esac; \
	done < .goreleaser.yaml > $(RELEASE_CONFIG)
	@printf "    %s: $(words $(PLUGIN_BINS)) plugin(s)\n" "$(RELEASE_CONFIG)"

# Writes the agent-only config by cutting the @server-only@ regions out of the
# source. A config rather than a command-line filter because goreleaser can
# skip builds but not archives, and a run that kept the server archive would
# fail looking for the server binaries it wraps.
RELEASE_CONFIG_AGENT := $(BUILD)/goreleaser.agent.yaml

.PHONY: release-config-agent
release-config-agent:
	@printf "==> Generating agent release config...\n"
	@mkdir -p $(BUILD)
	@awk '/@server-only-begin@/{cut=1; next} /@server-only-end@/{cut=0; next} !cut' \
	    .goreleaser.yaml > $(RELEASE_CONFIG_AGENT)

# Builds and archives only the agent, for every platform an agent runs on: the
# quick path when the change under test is agent-side. It is the same release
# pipeline as snapshot on the agent-only config, so the archives, names, and
# checksums match what a full snapshot would produce; what it skips is the
# staged web assets, the plugin probe and builds, and the cgo server targets,
# none of which the agent archive carries. --clean empties dist/ first, the
# same way snapshot does.
.PHONY: snapshot-agent
snapshot-agent: release-config-agent cross-image
	@printf "==> Building agent snapshot $(VERSION) ($(RELEASE_PARALLELISM) builds at once, -p=$(RELEASE_GO_PROCS), $(or $(RELEASE_MEMORY),no memory cap))...\n"
	@mkdir -p $(CACHE)/go
	$(GORELEASER_RUN) $(CROSS_IMAGE) release $(GORELEASER_FLAGS) --config $(RELEASE_CONFIG_AGENT) --snapshot --clean --skip=publish
	@printf "==> Agent archives are in dist/\n"

# Builds every artifact and leaves it in dist/, publishing nothing. This is the
# one to run to see what a release would contain.
.PHONY: snapshot
snapshot: release-config release-assets cross-image
	@printf "==> Building snapshot $(VERSION) ($(RELEASE_PARALLELISM) builds at once, -p=$(RELEASE_GO_PROCS), $(or $(RELEASE_MEMORY),no memory cap))...\n"
	@mkdir -p $(CACHE)/go
	$(GORELEASER_RUN) $(CROSS_IMAGE) release $(GORELEASER_FLAGS) --config $(RELEASE_CONFIG) --snapshot --clean --skip=publish
	@printf "==> Snapshot artifacts are in dist/\n"

# Publishes the tagged release. The tag has to exist and agree with the VERSION
# file, or the archives would be named one number and the binaries inside them
# would report another.
.PHONY: release
release: release-check test release-config release-assets cross-image
	@printf "==> Releasing $(VERSION) ($(RELEASE_PARALLELISM) builds at once, -p=$(RELEASE_GO_PROCS), $(or $(RELEASE_MEMORY),no memory cap))...\n"
	@mkdir -p $(CACHE)/go
	$(GORELEASER_RUN) --env-file $(RELEASE_ENV) $(CROSS_IMAGE) release $(GORELEASER_FLAGS) --config $(RELEASE_CONFIG) --clean

.PHONY: release-check
release-check:
	@if [ ! -f $(RELEASE_ENV) ]; then \
	  printf "Error: $(RELEASE_ENV) not found; it holds GITHUB_TOKEN=<token>\n" >&2; exit 1; \
	fi
	@tag=$$(git describe --exact-match --tags 2>/dev/null); \
	if [ -z "$$tag" ]; then \
	  printf "Error: HEAD is not tagged; tag it v$(VERSION) to release\n" >&2; exit 1; \
	fi; \
	if [ "$${tag#v}" != "$(VERSION)" ]; then \
	  printf "Error: tag $$tag does not match VERSION $(VERSION)\n" >&2; exit 1; \
	fi
	@if [ -n "$$(git status --porcelain)" ]; then \
	  printf "Error: working tree is dirty\n" >&2; exit 1; \
	fi
	@ha=$$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' homeassistant/custom_components/karakuri_assist/manifest.json); \
	if [ "$$ha" != "$(VERSION)" ]; then \
	  printf "Error: the Home Assistant integration's manifest.json says $$ha, not $(VERSION)\n" >&2; exit 1; \
	fi

# ── Lint ─────────────────────────────────────

.PHONY: lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		printf "golangci-lint not installed, skipping Go lint\n"; \
	fi
	cd frontend && npm run check

# ── Line count ────────────────────────────────

# ── Protocol ─────────────────────────────────
# proto regenerates the plugin wire protocol from pkg/plugin/proto/plugin.proto.
# It needs protoc, protoc-gen-go, and protoc-gen-go-grpc on PATH (or in
# $(GOPATH)/bin); the generated files are committed.

.PHONY: proto
proto:
	@printf "==> Generating plugin protocol...\n"
	cd pkg/plugin/proto && PATH="$$PATH:$$(go env GOPATH)/bin" protoc --proto_path=. \
	  --go_out=. --go_opt=paths=source_relative \
	  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	  plugin.proto

.PHONY: cloc
cloc:
	@cloc . --exclude-dir=node_modules,dist,.vite,.git,.claude,build,var \
	    --fullpath --not-match-f='\.pb\.go$$'

# ── Test ─────────────────────────────────────
# The default runs every unit-test group. TARGET narrows it to one component,
# while test-integration remains the tagged end-to-end HTTP suite. PKGS narrows
# the server group to the named Go packages. TARGET=tsdb runs the tagged
# time-series suite against the containers `make tsdb-up` starts, and
# TARGET=openhab the openHAB suite against the server `make openhab-up` starts.

SERVER_TEST_PKGS := ./cmd/karakuri ./internal/... ./pkg/... ./plugins/...
PKGS ?= $(SERVER_TEST_PKGS)

.PHONY: test test-all test-server test-frontend test-agent test-automation test-tsdb test-openhab
test:
	@case "$(TARGET)" in \
		"") target=test-all ;; \
		server|frontend|agent|automation|tsdb|openhab) target=test-$(TARGET) ;; \
		*) printf "Error: TARGET must be server, frontend, agent, automation, tsdb, or openhab\n" >&2; exit 1 ;; \
	esac; \
	$(MAKE) --no-print-directory $$target

test-all: test-server test-frontend test-agent test-automation

test-server:
	@printf "==> Testing server...\n"
	go test $(PKGS)
	@if [ "$(PKGS)" = "$(SERVER_TEST_PKGS)" ]; then python3 -m unittest discover -s homeassistant/tests; fi

test-frontend:
	@printf "==> Testing frontend...\n"
	cd frontend && npm run check
	cd frontend && npm test

test-agent:
	@printf "==> Testing agent...\n"
	go test ./cmd/karakuri-agent ./agent/...

test-automation:
	@printf "==> Testing automation command and module...\n"
	go test ./cmd/karakuri-auto
	cd automation && go test ./...

.PHONY: test-integration
test-integration:
	@printf "==> Running integration tests...\n"
	go test -tags karakuri_integration -count=1 -timeout 10m ./test/...

# The time-series suite talks to real databases, so it is tagged and needs
# the containers in test/tsdb/compose.yaml. tsdb-seed loads the fixture series
# the chart checks draw.
TSDB_COMPOSE := docker compose -f test/tsdb/compose.yaml -p karakuri-tsdb

test-tsdb:
	@printf "==> Running time-series integration tests...\n"
	go test -tags karakuri_tsdb -count=1 -timeout 10m ./test/tsdb/...

.PHONY: tsdb-up tsdb-down tsdb-seed
tsdb-up:
	$(TSDB_COMPOSE) up -d --wait

tsdb-down:
	$(TSDB_COMPOSE) down -v

tsdb-seed:
	go run -tags karakuri_tsdb ./test/tsdb/seed

# The openHAB suite talks to a real openHAB, so it is tagged and needs the
# server in test/openhab/compose.yaml; openhab-up also creates its API token.
OPENHAB_COMPOSE := OPENHAB_UID=$(shell id -u) OPENHAB_GID=$(shell id -g) \
	docker compose -f test/openhab/compose.yaml -p karakuri-openhab

test-openhab:
	@printf "==> Running openHAB integration tests...\n"
	go test -tags karakuri_openhab -count=1 -timeout 10m ./test/openhab/...

.PHONY: openhab-up openhab-down
openhab-up:
	$(OPENHAB_COMPOSE) up -d --wait
	sh test/openhab/setup.sh

openhab-down:
	$(OPENHAB_COMPOSE) down -v
	rm -f build/openhab-token

# Kept as the name people already type; the smoke cycle it used to run is now
# the first test of the integration suite.
.PHONY: smoke
smoke: test-integration
