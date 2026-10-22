---
name: datacodex-notify
description: Send WhatsApp notifications (text and voice) to the developer from the reviewer or manager session of a long multi-agent run, once the user has switched notifications on for the session. Use it at task start, at every review gate or finished group, when progress stalls, when an implementer stops or a test fails, when you need the developer's approval or are blocked, and when the task ends. Also start a heartbeat so a crash of the reviewer session is reported. Notifications are off until the user activates them. Implementer, delegate, and subagent sessions never use this skill.
---

# DataCodex Notify

DataCodex Notify is a small Windows tray app on this PC. It sends WhatsApp messages (text, and a voice note for important levels) to the developer so they do not have to watch the screen during long sessions. You talk to it through a command-line program.

## Who may use it

Only the **reviewer / manager session** sends notifications. If you are an implementer, a delegate, a subagent, or any session that another agent started to do part of the work (for example through `claude-delegate` or `codex-delegate`), **do not call this tool at all and ignore every DataCodex Notify block in the rules files**. The reviewer reports on your behalf.

## Notifications are off until the user turns them on

Never send a notification, and never run `connect`, until notifications are active for this session:

- The user may switch them on with a phrase such as "notify on" (the exact phrases are in the DataCodex Notify block of your rules file, if there is one), or by asking you directly.
- If your rules file says notifications are always on for manager sessions, activate them as soon as the session starts.
- For a long task you may suggest notifications once, in a single short line. Do not suggest again in the same session, and never turn them on yourself.

If your rules file contains a block between `<!-- datacodex-notify:start v1 -->` and `<!-- datacodex-notify:end -->`, follow it: it lists which events to report, at which level, in which language, and whether to run a heartbeat. This skill describes the commands that block refers to.

## The program

Always call the console build, with its full path (it is not on PATH):

```
{{INSTALL_DIR}}\datacodex-notify-cli.exe
```

The tray app itself is `{{INSTALL_DIR}}\datacodex-notify.exe`; you never start or stop it. If it is not running the CLI exits with code 2 (see below).

Do not change the app: never edit its settings, recipients, keys, or files, and never try to link or unlink WhatsApp. If something looks wrong, say so in your report to the developer instead.

## One-off message or task activation

There are two different cases. Never mix them up.

- **A one-off message the user asks for directly** ("send me a test message", "message me when this is done"): run only `send`, with `--wait 20`. Do **not** run `connect`, `disconnect`, or `heartbeat` for it. Add `--session` only if a session is already active in this conversation. Never connect and disconnect around a single message.
- **Activating notifications for a task or a conversation**: `connect` **once** (next section), then use the same session name for the whole conversation. Disconnect only on the user's deactivation phrase or when the task ends.

## Activating and deactivating a session

When notifications become active:

1. Pick a unique session name once: the project name plus a short random suffix, for example `myproject-k7f2`. Reuse exactly this name for the whole conversation; never pick a new one.
2. Register the session, passing the project folder name with `--project` and the absolute path of every rules file that holds the DataCodex Notify block:

```powershell
& $cli connect --source claude-code --session "myproject-k7f2" --project "myproject" --rules-file "$env:USERPROFILE\.claude\CLAUDE.md"
```

`--source` is `claude-code`, `codex`, `gemini`, or `other`. The program sends the developer a short "connected" notice. Then pass `--session` with the same name on every `send` and `heartbeat`, so the developer can see and manage the session.

When the user says a deactivation phrase, or the task ends and you hand control back (never after a single message):

```powershell
& $cli heartbeat-stop --id "myproject-k7f2"
& $cli disconnect --source claude-code --session "myproject-k7f2"
```

`disconnect` also removes the session's heartbeats. After it, send nothing more in this session.

## When to send, and at which level

Unless your rules file says otherwise:

| Moment | Level |
|--------|-------|
| Task started (plan summary and number of steps) | `info` |
| Review gate reached, or a group finished | `info` |
| Every few steps or every few minutes of real progress | `info` |
| An implementer printed a STOP marker, a test failed, or the executor was switched because of a quota | `warning` |
| Waiting for the developer's approval to continue | `warning` (use `critical` if you have been blocked for a long time) |
| A stop condition from the global rules (three failed builds in a row, a broken core feature, a security problem) | `critical` |
| Task finished (short summary and the next step) | `info` |

Do not send more often than the work justifies. Identical messages from the same source are dropped for a few minutes, and the app limits messages per hour, so spamming only hides the important ones.

## Writing the message

- Short: keep the body to one to three lines. Put the one-line summary in `--title` and short facts (phase, tests, step) in `--field`; the app lays them out with a header, separators, and the time.
- Written in the language your rules file asks for (by default **Egyptian Arabic**, colloquial and plain, because the developer reads it on a phone).
- Say what happened and what, if anything, you need from the developer.
- **Never** include code, file contents, secrets, tokens, passwords, or customer data.
- Use the same `--source` value as in `connect` on every command (`claude-code`, `codex`, `gemini`, or `other`). The project is already in the session name.

## Sending

**Always write the message to a UTF-8 file without a BOM and pass it with `--text-file`.** Typing Arabic directly on a command line is unreliable on Windows. Use your file-writing tool to create the temporary file, because it writes UTF-8 without a BOM. (The program removes a BOM if one is present, but do not rely on shell redirection or `Set-Content` for Arabic text.)

PowerShell:

```powershell
$cli  = "{{INSTALL_DIR}}\datacodex-notify-cli.exe"
$file = Join-Path $env:TEMP "dn-message.txt"
# Create $file with your file-writing tool (UTF-8, no BOM), then:
& $cli send --level info --source claude-code --session "myproject-k7f2" --text-file $file
Remove-Item $file
```

Bash (Git Bash):

```bash
CLI="{{INSTALL_DIR_BASH}}/datacodex-notify-cli.exe"
FILE="$(mktemp)"
# Create "$FILE" with your file-writing tool (UTF-8, no BOM), then:
"$CLI" send --level warning --source claude-code --session "myproject-k7f2" --text-file "$FILE"
rm -f "$FILE"
```

Useful options:

- `--level info|warning|critical` (required).
- `--text-file <path>` (or `--text "..."` for plain ASCII).
- `--session <name>`: the session name used with `connect`. The session remembers the project, so `send` needs no `--project`.
- `--project <name>`: the project name: the git repository root folder of the current work (`git rev-parse --show-toplevel`, last part), or the working folder name when not in git. Leave it out if the name looks temporary (a temp, cache, or pasted-files folder) or there is no real project. Give it to `connect`; a one-off `send` without a session may pass it too. Messages show the agent and the project in their header.
- `--title <text>`: a one-line summary shown above the body (up to 120 characters). Do not use formatting marks in it.
- `--field key=value`: a short fact shown under the body (repeatable, at most 8; key up to 40 and value up to 200 characters). Do not use formatting marks in keys or values.
- `--speech-file <path>`: a shorter text to speak when a voice note is sent. Optional; by default the message text is spoken.
- `--source <agent>`: the same value as in `connect`.
- `--dedupe-key <key>`: use the same key for messages that must count as one.
- `--to <name or number>`: only when the developer told you to address someone specific.
- `--wait <seconds>` (0 to 60): wait until the message is sent (or delivered) or the time runs out; the JSON line then carries the final status. Use `--wait 20` for one-off messages the user asked for; do not use it for progress reports.
- `--mode text|voice|both`: only when your rules file tells you to; otherwise leave it alone and the app decides.

The program prints one JSON line with the message id and its status (`queued`, `deduplicated`, `rate_limited`, `held_quiet_hours`, or `held_paused`).

## Exit codes

| Code | Meaning | What you do |
|------|---------|-------------|
| 0 | Accepted (queued or held on purpose) | Continue. |
| 2 | The app is not running | **Continue the task.** Do not retry in a loop. Mention in your final report that notifications were not available. |
| 3 | Authentication failed | Continue, and tell the developer in your final report. Do not try to fix it. |
| 4 | The request was invalid (missing level, empty text, bad option) | Fix the command and send once more. |
| 5 | Duplicate or rate limited | Not an error. Continue. |
| 6 | The user turned notifications off for this session or for all agents | **Stop sending notifications for the rest of this session. Do not retry**, and do not start a heartbeat. |
| 7 | Only with `--wait`: the message is still queued after the wait | Not an error. Continue. |

A failed notification must never fail or stop the task you are doing.

## Heartbeat (dead-man's switch)

A heartbeat makes the app alert the developer if you stop reporting in, for example when the reviewer session crashes.

1. After activating, register one heartbeat with a stable id (for example the session name), then keep it alive:

```powershell
& $cli heartbeat --id "myproject-k7f2" --every 600 --grace 300 --label "My project" --source claude-code --session "myproject-k7f2"
```

2. While the task runs, report in again at least every `--every` seconds (a good rhythm is every few minutes, for example at every step), with only the id and session:

```powershell
& $cli heartbeat --id "myproject-k7f2" --session "myproject-k7f2"
```

3. When the task ends, or when you deactivate, stop it so no false alarm is raised:

```powershell
& $cli heartbeat-stop --id "myproject-k7f2"
```

If a heartbeat was already reported as lost and you beat again, the developer automatically receives a short "back" message.

## Checking the app

`& $cli status` prints one JSON line with the connection state (`connected`, `unlinked`, and so on). Use it only if you need to know why messages are not arriving; do not poll it.
