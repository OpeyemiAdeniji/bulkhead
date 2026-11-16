.DEFAULT_GOAL := help
.PHONY: help setup lint secrets hooks-update validate validate-exceptions test-contract test-exceptions chart-test test-isolation cluster-up cluster-down tenants-apply kyverno-up policy-test test-policy-live checker-build checker-test

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

setup: ## Install the git hooks (pre-commit is installed with pipx)
	@command -v pre-commit >/dev/null || (echo 'Install pre-commit first: pipx install pre-commit'; exit 1)
	pre-commit install --install-hooks
	pre-commit install --hook-type commit-msg

lint: ## Run every pre-commit hook against all files
	pre-commit run --all-files

secrets: ## Scan the full git history for leaked secrets
	gitleaks git --redact

hooks-update: ## Bump pinned hook versions in .pre-commit-config.yaml
	pre-commit autoupdate

validate: ## Validate the tenant files against the contract
	python3 scripts/validate_tenants.py tenants/*.yaml

test-contract: ## Run the contract tests (valid files pass, invalid files fail)
	bash tests/contract/run.sh

validate-exceptions: ## Validate the policy exceptions in exceptions/
	python3 scripts/validate_exceptions.py $(wildcard exceptions/*.yaml)

test-exceptions: ## Run the exception tests (valid files pass, invalid files fail)
	bash tests/exceptions/run.sh

cluster-up: ## Build the kind cluster and install Calico
	bash scripts/cluster-up.sh

cluster-down: ## Delete the kind cluster
	bash scripts/cluster-down.sh

chart-test: ## Lint and render the tenant chart for every tenant file
	bash scripts/chart-test.sh

tenants-apply: ## Render every tenant file and apply it to the local cluster
	bash scripts/tenants-apply.sh

test-isolation: ## Run the isolation tests against the local cluster
	bash tests/isolation/run.sh

kyverno-up: ## Install Kyverno and the bulkhead-platform chart into the local cluster
	bash scripts/kyverno-install.sh

policy-test: ## Test the Kyverno policies offline, and check that the tests can fail
	bash scripts/policy-test.sh

test-policy-live: ## Test the Kyverno policies on the local cluster (set WITH_EXPIRY=1 to test expiry)
	bash tests/policy-live/run.sh

checker-build: ## Build the checker into bin/bulkhead
	go build -o bin/bulkhead ./cmd/bulkhead

checker-test: ## Format check, vet and unit tests of the checker (CI also runs the race detector, govulncheck and golangci-lint)
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "run gofmt -w on:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go test -count=1 ./...
