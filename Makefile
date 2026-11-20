# Qaphi — compilación, tests y compilación cruzada.
#
#   make            compila para el sistema actual en bin/
#   make check      gofmt + go vet + go test
#   make cross      compila para todas las plataformas objetivo en dist/
#   make size       muestra el tamaño de los binarios de dist/
#   make clean      borra bin/ y dist/
#
# Versiones: se publican con una etiqueta semver, p. ej.
#   git tag -a v0.2.0 -m "Qaphi v0.2.0" && make cross
#
# Todos los binarios se compilan sin CGO, con -trimpath y sin símbolos de
# depuración (-s -w).

GO      ?= go
PKG     := ./cmd/qaphi
BIN_DIR := bin
DIST    := dist

# Versión: etiqueta semver del commit (v0.1.0…). Sin etiqueta exacta, o con
# cambios sin confirmar, git describe da algo como v0.1.0-3-gabc123[-dirty],
# que Qaphi trata como compilación de desarrollo («dev»).
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null)
# Fecha del último commit (no la hora de compilación) para builds reproducibles.
DATE    ?= $(shell git log -1 --format=%cI 2>/dev/null)

VPKG    := qaphi/internal/version
LDFLAGS := -s -w -X $(VPKG).Version=$(VERSION) -X $(VPKG).Commit=$(COMMIT) -X $(VPKG).Date=$(DATE)
BUILD   := $(GO) build -trimpath -ldflags '$(LDFLAGS)'

export CGO_ENABLED := 0

# Plataformas objetivo: solo Linux.
TARGETS := \
	$(DIST)/qaphi-linux-amd64 \
	$(DIST)/qaphi-linux-arm64 \
	$(DIST)/qaphi-linux-armv7 \
	$(DIST)/qaphi-linux-armv6

.PHONY: all build check fmt fmt-check vet test cross size clean run $(TARGETS)

all: build

build:
	$(BUILD) -o $(BIN_DIR)/qaphi $(PKG)

run: build
	./$(BIN_DIR)/qaphi serve --log-level=debug

check: fmt-check vet test

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "Ficheros sin formatear:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

cross: $(TARGETS)

$(DIST)/qaphi-linux-amd64:
	GOOS=linux GOARCH=amd64 $(BUILD) -o $@ $(PKG)

$(DIST)/qaphi-linux-arm64:
	GOOS=linux GOARCH=arm64 $(BUILD) -o $@ $(PKG)

$(DIST)/qaphi-linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 $(BUILD) -o $@ $(PKG)

# ARMv6 (Raspberry Pi Zero / 1), que no puede ejecutar el binario armv7.
$(DIST)/qaphi-linux-armv6:
	GOOS=linux GOARCH=arm GOARM=6 $(BUILD) -o $@ $(PKG)

size:
	@for f in $(TARGETS); do \
		if [ -f "$$f" ]; then \
			wc -c < "$$f" | awk -v f="$$f" '{ printf "%-32s %10d bytes  %6.2f MiB\n", f, $$1, $$1 / 1048576 }'; \
		fi; \
	done

clean:
	rm -rf $(BIN_DIR) $(DIST)
