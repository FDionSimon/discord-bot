.PHONY: tidy run build test fmt vet lint

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

# Loads .env into the environment, then runs the bot.
run:
	set -a; . ./.env; set +a; go run ./

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/bot ./

lint: fmt vet test