# Qaphi — compilación, tests y compilación cruzada.
#
#   make            compila para el sistema actual en bin/
#   make check      gofmt + go vet + go test
#   make cross      compila para todas las plataformas objetivo en dist/
#   make release    cross + SHA256SUMS (p. ej. make release VERSION=v0.3.1)
#   make size       muestra el tamaño de los binarios de dist/
#   make clean      borra bin/ y dist/
#
# Versiones: se publican con una etiqueta semver, p. ej.
#   git tag -a v0.2.0 -m "Qaphi v0.2.0" && make release
#
# Los binarios llevan la versión en el nombre, sin la «v» y con guiones bajos
# en lugar de puntos: v0.3.1 -> dist/qaphi_0_3_1-linux-amd64.
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

VPKG    := github.com/future-chocolate6122/qaphi/internal/version
LDFLAGS := -s -w -X $(VPKG).Version=$(VERSION) -X $(VPKG).Commit=$(COMMIT) -X $(VPKG).Date=$(DATE)
BUILD   := $(GO) build -trimpath -ldflags '$(LDFLAGS)'

export CGO_ENABLED := 0

# Versión en el nombre de los binarios: v0.3.1 -> 0_3_1.
FILEVER := $(subst .,_,$(patsubst v%,%,$(VERSION)))
NAME    := $(DIST)/qaphi_$(FILEVER)

# Plataformas objetivo: solo Linux.
TARGETS := \
	$(NAME)-linux-amd64 \
	$(NAME)-linux-arm64 \
	$(NAME)-linux-armv7 \
	$(NAME)-linux-armv6

.PHONY: all build check fmt fmt-check vet test cross release size clean run $(TARGETS)

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

# release: binarios de todas las plataformas y su SHA256SUMS, listos para
# subir a GitHub Releases.
release: cross
	cd $(DIST) && sha256sum $(notdir $(TARGETS)) > SHA256SUMS_$(FILEVER)
	@cat $(DIST)/SHA256SUMS_$(FILEVER)

$(NAME)-linux-amd64:
	GOOS=linux GOARCH=amd64 $(BUILD) -o $@ $(PKG)

$(NAME)-linux-arm64:
	GOOS=linux GOARCH=arm64 $(BUILD) -o $@ $(PKG)

$(NAME)-linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 $(BUILD) -o $@ $(PKG)

# ARMv6 (Raspberry Pi Zero / 1), que no puede ejecutar el binario armv7.
$(NAME)-linux-armv6:
	GOOS=linux GOARCH=arm GOARM=6 $(BUILD) -o $@ $(PKG)

size:
	@for f in $(TARGETS); do \
		if [ -f "$$f" ]; then \
			wc -c < "$$f" | awk -v f="$$f" '{ printf "%-32s %10d bytes  %6.2f MiB\n", f, $$1, $$1 / 1048576 }'; \
		fi; \
	done

clean:
	rm -rf $(BIN_DIR) $(DIST)
