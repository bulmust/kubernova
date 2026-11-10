.PHONY: build test lint run clean

build:
	go build -o kubernova .

test:
	go vet ./...
	go test -race ./...

lint:
	golangci-lint run

run:
	go run .

clean:
	rm -f kubernova
