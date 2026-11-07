.PHONY: test test-unit test-live test-frontend lint db-up db-down seed seed-reset frontend-deps frontend-dev

BACKEND := backend
FRONTEND := frontend

# Layers 1 and 3: no Docker, no API key.
test-unit:
	cd $(BACKEND) && go test -race ./...

# Layers 1-4: starts Postgres first. No API key needed.
# -count=1 disables the test cache: Go cannot see external state (DB,
# mock provider), so a cached "ok" could hide a broken environment.
test: db-up test-frontend
	cd $(BACKEND) && go test -race -count=1 -tags integration ./...

# Frontend unit and component tests (Vitest, fake fetch and EventSource).
test-frontend: frontend-deps
	cd $(FRONTEND) && npm test

# Layer 5: real API, paid (at most 2 calls, SDK retries off). Needs
# ANTHROPIC_API_KEY in the environment or .env. Run only with approval.
test-live:
	cd $(BACKEND) && go test -count=1 -v -tags live -run '^TestLive' ./internal/llm/

lint: frontend-deps
	@unformatted=$$(cd $(BACKEND) && gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	cd $(BACKEND) && go vet ./... && go vet -tags integration ./... && go vet -tags live ./...
	cd $(FRONTEND) && npm run lint

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

# Applies migrations and loads sample data (skipped if already seeded).
seed: db-up
	cd $(BACKEND) && go run ./cmd/seed

# Drops all tables and reseeds from scratch.
seed-reset: db-up
	cd $(BACKEND) && go run ./cmd/seed -reset

# Installs the exact versions in package-lock.json once.
frontend-deps:
	@test -d $(FRONTEND)/node_modules || (cd $(FRONTEND) && npm ci)

# Vite dev server on :5173; /api is proxied to the Go API on :8080.
frontend-dev: frontend-deps
	cd $(FRONTEND) && npm run dev
