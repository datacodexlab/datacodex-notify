## What changed

<!-- One or two sentences. Link the issue if there is one. -->

## Why

## How it was tested

- [ ] `gofmt -l .` prints nothing
- [ ] `go vet ./...` and `go vet -tags devfake ./...`
- [ ] `go test -short ./...` and `go test -short -tags devfake ./...`
- [ ] UI change: ran `go test ./internal/ui` and regenerated the images with `go run ./tools/docsgen`

## Checklist

- [ ] No Arabic text in code files (translations only in the i18n files)
- [ ] No real phone numbers, keys, tokens, or personal paths
- [ ] Docs and changelog updated when behavior changed
