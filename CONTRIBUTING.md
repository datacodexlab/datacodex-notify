# Contributing to DataCodex Notify

Thanks for helping. Small, tested changes are the easiest to review.

## Workflow

1. Fork the repository and create a branch from `main` (`fix-quiet-hours-label`, `feature-gemini-voices`, ...).
2. Make the change. Keep it focused; unrelated cleanups belong in their own pull request.
3. Run the checks below and make sure they pass.
4. Open a pull request against `main` and fill in the template. Explain what changed and why.

## Checks that must pass

```powershell
gofmt -l .                           # must print nothing
go vet ./...
go vet -tags devfake ./...
go test -short ./...
go test -short -tags devfake ./...   # uses a fake WhatsApp sender; never touches the network
```

`go test ./internal/ui` additionally renders every tab in headless Microsoft Edge and needs Node.js. Running the race detector (`go test -race`) needs a C compiler and is optional locally.

CI runs the same checks on Windows for every pull request.

## Code rules

- **No Arabic text in code files.** Code, comments, and log messages are English only. Arabic user-facing text lives in the translation files (`web/i18n/ar.json`, `internal/locale/ar.json`). This avoids encoding problems.
- Match the style of the surrounding code: names, comment density, error handling.
- Do not add a dependency without discussing it in an issue first. The release binaries are pure Go (`CGO_ENABLED=0`); `THIRD_PARTY_NOTICES.md` is regenerated with `go run ./tools/notices` when dependencies change.
- Never put real phone numbers, API keys, tokens, or personal paths in code, tests, or docs. Use clearly fake values (`+201000000001`, `test-key-A-0123456789`, `C:\Users\demo`).
- Security-sensitive changes (the local API, token handling, key storage) need tests.

## Documentation images

Screenshots, chat mockups, and the architecture diagram are generated from the code, never edited by hand. After any UI or message-format change, regenerate them from a clean working tree:

```powershell
go run ./tools/docsgen
```

It needs Go, Node.js, and Microsoft Edge. See the README, "Contributing".

## Reporting problems

Bugs and feature ideas: use the issue templates. Security problems: do not open a public issue, see [SECURITY.md](SECURITY.md).

By contributing you agree that your contribution is licensed under the GNU General Public License, version 3 only (GPL-3.0-only), the license of this project. Every Go file starts with the line `// SPDX-License-Identifier: GPL-3.0-only`; keep it in new files.
