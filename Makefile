.PHONY: fmt tidy vet lint generate check-ts test-ts build-assets inject test test-race test-e2e test-e2e-browser test-e2e-browser-headful

fmt:
	gofmt -w $$(rg --files -g '*.go')

tidy:
	go mod tidy

vet:
	go vet ./...

lint: vet check-ts

generate:
	go generate ./...

check-ts:
	npm exec tsc -- --noEmit

test-ts:
	npm test

build-assets:
	npm run build

inject: generate
	npm ci
	$(MAKE) check-ts build-assets

test:
	go test ./...

test-race:
	go test -race ./...

test-e2e:
	go test ./e2e/...

test-e2e-browser:
	CDP_E2E_BROWSER=1 CDP_E2E_HEADLESS=1 go test -v -count=1 ./e2e/scenarios/...

test-e2e-browser-headful:
	CDP_E2E_BROWSER=1 CDP_E2E_HEADLESS=0 go test -v -count=1 ./e2e/scenarios/...
