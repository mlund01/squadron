SQUADRON_TEST_ROOT ?= $(CURDIR)/.local-testing
ROOT := $(abspath $(SQUADRON_TEST_ROOT))
BINARY := $(ROOT)/bin/squadron
SCENARIOS ?= routing delegation failure missing-input
TEST_PACKAGES := ./config/... ./mission/... ./agent/... ./aitools/... ./store/... ./humaninput/... ./gateway/... ./mcphost/...

ifneq ($(filter verify start,$(MAKECMDGOALS)),)
ifeq ($(CONFIG),)
$(error Usage: make verify|start CONFIG=/path/to/test-config)
endif
CONFIG_DIR := $(abspath $(CONFIG))
endif

.PHONY: build test run verify start

build:
	mkdir -p "$(ROOT)/bin"
	go build -o "$(BINARY)" ./cmd/cli

test: build
	go test $(TEST_PACKAGES)
	@$(MAKE) --no-print-directory run

# Each scenario gets a fresh run directory so an earlier database can't make it pass.
run: build
	@mkdir -p "$(ROOT)/runs"
	@for s in $(SCENARIOS); do \
	  f="$(CURDIR)/examples/local-testing/$$s.json"; \
	  [ -f "$$f" ] || { echo "Unknown scenario: $$s" >&2; exit 2; }; \
	  out=$$(mktemp -d "$(ROOT)/runs/$$s.XXXXXX"); \
	  python3 scripts/local-test.py "$$f" --binary "$(BINARY)" --output "$$out/result" || exit 1; \
	done

# --squadron-home pins the config's own state, so an inherited SQUADRON_HOME can't point it at another instance.
verify: build
	cd "$(CONFIG_DIR)" && "$(BINARY)" --squadron-home "$(CONFIG_DIR)/.squadron" verify .

start: build
	cd "$(CONFIG_DIR)" && exec "$(BINARY)" --squadron-home "$(CONFIG_DIR)/.squadron" engage --foreground --headless --init -c .
