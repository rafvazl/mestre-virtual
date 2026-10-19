# Automação local. Só depende do toolchain Go (stdlib).
GO        ?= go
BIN       ?= bin/mestre-virtual
FUZZTIME  ?= 20s
COVERFILE ?= coverage.out

.PHONY: all build test test-race vet fmt fmt-check fuzz cover ci clean

all: ci build

build:
	$(GO) build -o $(BIN) ./cmd/mestre-virtual

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "arquivos fora do gofmt:"; echo "$$out"; exit 1; fi

# Fuzz curto do parser de saída do LLM (ajuste com FUZZTIME=2m).
fuzz:
	$(GO) test -run='^$$' -fuzz=FuzzParseResponse -fuzztime=$(FUZZTIME) ./internal/llm

cover:
	$(GO) test -coverprofile=$(COVERFILE) ./...
	$(GO) tool cover -func=$(COVERFILE) | tail -n 1

# O mesmo que o CI roda (exceto fuzz).
ci: fmt-check vet test-race

clean:
	rm -rf bin $(COVERFILE)
