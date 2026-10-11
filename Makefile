BIN := bin/scoutjobs
PKG := ./...
# local builds carry the git version with a -dev suffix: shown in the dashboard, never replaced by scoutjobs update
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')-dev
LDFLAGS := -X github.com/zoolcoder/scoutjobs/internal/cli.Version=$(VERSION)

.PHONY: build run test test-unit lint fmt clean install dash docs docs-serve skills evals

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/scoutjobs

run: build
	$(BIN) $(ARGS)

dash: build
	$(BIN) serve

test:
	go test $(PKG)

test-unit:
	go test -short $(PKG)

lint:
	go vet $(PKG)
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, go vet only"

fmt:
	gofmt -l -w cmd internal

install: build
	mkdir -p $(HOME)/.local/bin
	cp $(BIN) $(HOME)/.local/bin/scoutjobs.new
	mv $(HOME)/.local/bin/scoutjobs.new $(HOME)/.local/bin/scoutjobs
	@echo "installed $(VERSION) to ~/.local/bin/scoutjobs (add to PATH if needed); a running dashboard restarts on it"

skills: ## regenerate the agent mirror skills from agents/*.md
	sh scripts/agents-to-skills.sh

evals: ## run the plugin eval suite (evals/), needs the claude CLI
	claude plugin eval . --no-publish --threshold 0.8

docs:
	[ -d node_modules ] || npm ci
	npx antora antora-playbook.yml

docs-serve: docs
	npx --yes http-server build/site -p 8080 -c-1

clean:
	rm -rf bin dist build
