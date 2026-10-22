# Changelog

All notable changes to DataCodex Notify are recorded here. Format: [Keep a Changelog](https://keepachangelog.com/). Versioning: [SemVer](https://semver.org/).

## [1.0.0] - first public release

### Features
- Windows tray app that sends WhatsApp text messages and voice notes from AI agents (Claude Code, Codex, Gemini) through a linked-device session.
- Command-line tool: `send` (with `--title`, `--field`, `--project`, `--wait`), `connect`, `disconnect`, `heartbeat`, `heartbeat-stop`, `status`, `open`, with documented exit codes.
- Clear message layout: agent and project in the header, optional title and key/value facts, local time; strong markers for warning and critical.
- Voice notes with Gemini speech: your own keys with ordered fallback, selectable models, normal / fast / very fast speed, optional spoken intro, local cache. Voice falls back to text when it cannot be produced.
- Connect an agent in three steps: generated prompt, template cards (calm, normal, detailed), per-event level and mode, installable skill for Claude Code and Codex.
- Rules: quiet hours with a digest, pause, duplicate suppression, per-hour limit, per-agent and global mute, heartbeat (dead-man's switch), and per-alert switches and modes.
- Admin window in Arabic and English (right-to-left aware): Home with status and recent messages, Agents, Alerts, Voice, Settings; the first-run checklist; on-demand pairing code.
- Local-first security: API on `127.0.0.1` only, bearer token and Gemini keys in Windows Credential Manager, token rotation.
- Windows installer (per-user, no admin) and a portable zip.
- Licensed under the GNU General Public License, version 3 only (GPL-3.0-only); see `LICENSE` and `THIRD_PARTY_NOTICES.md`.
