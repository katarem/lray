BIN      := lray
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
PREFIX   ?= $(HOME)/.local
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build install uninstall cross snapshot tidy clean

## build: compila el binario en bin/
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) .

## install: instala en $(PREFIX)/bin (por defecto ~/.local/bin)
install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/$(BIN) $(PREFIX)/bin/$(BIN)
	@echo "Instalado en $(PREFIX)/bin/$(BIN)"

uninstall:
	rm -f $(PREFIX)/bin/$(BIN)

## cross: compila para todas las plataformas en dist/ sin GoReleaser
cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "-> $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/$(BIN)_$${os}_$${arch}/$(BIN)$$ext . || exit 1; \
	done

## snapshot: prueba la release completa en local (requiere goreleaser)
snapshot:
	goreleaser release --snapshot --clean

tidy:
	go mod tidy

clean:
	rm -rf bin dist
