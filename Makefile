VERSION ?= dev

.PHONY: build web test lint image dev clean

## build: build the web UI and the flowmon binary
build: web
	CGO_ENABLED=1 go build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/flowmon ./cmd/flowmon

## web: build the frontend into web/dist (embedded by the Go binary)
web:
	cd web && npm ci && npm run build

## test: run Go tests and frontend lint
test:
	go vet ./...
	go test -race ./...
	cd web && npm run lint

## image: build the container image
image:
	docker build --build-arg VERSION=$(VERSION) -t flowmon:$(VERSION) .

## dev: run the Go server on :8080; run `npm run dev` in web/ for the UI with hot reload
dev:
	FLOWMON_DATA_DIR=./data go run ./cmd/flowmon serve

clean:
	rm -rf bin web/dist/assets web/dist/index.html web/dist/favicon.svg
