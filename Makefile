build:
	go build ./...

# Run unit tests
test:
	env "GORACE=halt_on_error=1" go test -v -race ./...

# Format the code
fix:
	find . -iname '*.go' -not -path '*/vendor/*' -print0 | xargs -0 gofmt -s -w
	find . -iname '*.go' -not -path '*/vendor/*' -print0 | xargs -0 goimports -w

# Lint the code
lint:
	golangci-lint run

setup_ci:
	GO111MODULE=on go get github.com/golangci/golangci-lint/cmd/golangci-lint@1.17.1
