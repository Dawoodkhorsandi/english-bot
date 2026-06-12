.PHONY: build test lint fmt vet run tidy

# Build the bot binary.
build:
	go build -o english-bot ./cmd/english-bot

# Run the full test suite.
test:
	go test ./... -count=1

# Run the linter (requires golangci-lint v2; see https://golangci-lint.run).
lint:
	golangci-lint run ./...

# Apply formatting (gofmt + goimports via golangci-lint's formatters).
fmt:
	golangci-lint fmt ./...

vet:
	go vet ./...

# Run the bot locally (reads config from the environment / .env).
run:
	go run ./cmd/english-bot

tidy:
	go mod tidy
