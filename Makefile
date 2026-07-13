.PHONY: test build frontend backend clean

test:
	go test ./...
	cd frontend && npm run build

frontend:
	cd frontend && npm run build

backend:
	go build -o ai-watch ./cmd/ai-watch

build: frontend backend

clean:
	rm -rf frontend/dist ai-watch coverage
