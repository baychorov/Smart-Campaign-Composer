.PHONY: test test-unit lint db-up db-down

BACKEND := backend

# Layers 1 and 3: no Docker, no API key.
test-unit:
	cd $(BACKEND) && go test -race ./...

# Layers 1-4: starts Postgres first. No API key needed.
# -count=1 disables the test cache: Go cannot see external state (DB,
# mock provider), so a cached "ok" could hide a broken environment.
test: db-up
	cd $(BACKEND) && go test -race -count=1 -tags integration ./...

lint:
	@unformatted=$$(cd $(BACKEND) && gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	cd $(BACKEND) && go vet ./... && go vet -tags integration ./...

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down
