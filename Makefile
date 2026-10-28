BINARY   := template-go
GO       := go
GOFLAGS  :=

.PHONY: all build run tidy clean reset-session

## Default target
all: build

## Download & tidy dependencies, then build
build: tidy
	$(GO) build $(GOFLAGS) -o $(BINARY) .

## Run directly (no binary produced)
run:
	$(GO) run $(GOFLAGS) .

## Tidy + download dependencies
tidy:
	$(GO) mod tidy

## Remove build artifacts
clean:
	rm -f $(BINARY)

## Delete the WhatsApp session (force re-login)
reset-session:
	rm -f db/session/template-session.db db/session/template-session.db-wal db/session/template-session.db-shm
	@echo "Session cleared. Run 'make run' to re-login."
