.PHONY: run test demo build

ADMIN_ENV = SURVEY_ADMIN_EMAIL=admin@example.com SURVEY_ADMIN_PASSWORD=change-me-please

build:
	go build -o bin/surveyd ./cmd/surveyd

run:
	$(ADMIN_ENV) go run ./cmd/surveyd

demo:
	go run ./cmd/demo

test:
	go test -race ./...
