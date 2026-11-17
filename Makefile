.PHONY: configure dev down install lint typecheck test test-integration test-go build
configure:
	node scripts/configure.mjs
install:
	pnpm install --frozen-lockfile
dev: configure
	docker compose up --build -d --wait
down:
	docker compose down
lint:
	pnpm lint
typecheck:
	pnpm typecheck
test:
	pnpm test
	$(MAKE) test-go
test-go:
	cd agents/node-agent && go test -race ./...
	cd services/scheduler && go test -race ./...
test-integration:
	pnpm test:integration
build:
	pnpm build
	cd agents/node-agent && go build -o ../../bin/opencompute-agent .
	cd services/scheduler && go build -o ../../bin/opencompute-scheduler .
