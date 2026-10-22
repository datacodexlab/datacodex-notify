// SPDX-License-Identifier: GPL-3.0-only

package agentprompt

import (
	"fmt"
	"strings"
)

// Exact markers of the block an agent writes into its rules file.
const (
	StartMarker = "<!-- datacodex-notify:start v1 -->"
	EndMarker   = "<!-- datacodex-notify:end -->"
)

var eventText = map[Event]string{
	EventTaskStart:          "A task starts (include a short plan summary and the number of steps)",
	EventTaskEnd:            "The task is finished (short summary and the next step)",
	EventFailure:            "A build or test fails, or an error stops progress",
	EventBlocked:            "You are blocked or need the user's approval to continue",
	EventCriticalStop:       "A stop condition: three failed builds in a row, a broken core feature, or a security problem",
	EventPhaseFinished:      "A phase, review gate, or group of steps is finished",
	EventImplementerStopped: "An implementer or delegate reported a STOP, or the executor was switched because of a quota",
	EventStepMilestone:      "A step or milestone is completed",
}

var languageText = map[Language]string{
	LanguageEgyptian: "short Egyptian Arabic (colloquial, plain words)",
	LanguageArabic:   "short, clear Modern Standard Arabic",
	LanguageEnglish:  "short, clear English",
}

var sourceName = map[Agent]string{
	AgentClaudeCode: "claude-code",
	AgentCodex:      "codex",
	AgentGemini:     "gemini",
	AgentOther:      "other",
}

// RulesFileName returns the rules file name for the agent.
func RulesFileName(o Options) string {
	switch o.Agent {
	case AgentClaudeCode:
		return "CLAUDE.md"
	case AgentCodex:
		return "AGENTS.md"
	case AgentGemini:
		return "GEMINI.md"
	default:
		return strings.TrimSpace(o.OtherFile)
	}
}

// rulesFileHint says where the rules file is, for the agent to resolve.
func rulesFileHint(o Options) string {
	name := RulesFileName(o)
	if o.Scope == ScopeProject {
		return fmt.Sprintf("`%s` in the root folder of the current project (create it if it does not exist)", name)
	}
	switch o.Agent {
	case AgentClaudeCode:
		return "`CLAUDE.md` inside the `.claude` folder of the user's home folder (create it if it does not exist)"
	case AgentCodex:
		return "`AGENTS.md` inside the `.codex` folder of the user's home folder (create it if it does not exist)"
	case AgentGemini:
		return "`GEMINI.md` inside the `.gemini` folder of the user's home folder (create it if it does not exist)"
	default:
		return fmt.Sprintf("the global (user-level) rules file named `%s` in this agent's own configuration folder (create it if it does not exist)", name)
	}
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = `"` + item + `"`
	}
	return strings.Join(quoted, ", ")
}

// Block returns the text an agent writes into its rules file, markers included.
func Block(o Options, env Env) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	source := sourceName[o.Agent]
	cli := env.CLIPath
	var b strings.Builder
	line := func(format string, args ...interface{}) { fmt.Fprintf(&b, format+"\n", args...) }

	line("%s", StartMarker)
	line("## DataCodex Notify (WhatsApp notifications)")
	line("")
	line("**Who this applies to.** Only the main (manager or reviewer) session follows this block. If you are a subagent, an implementer, a delegate, or any session started by another agent, ignore this whole block and never call DataCodex Notify.")
	line("")

	if o.Agent == AgentClaudeCode {
		line("**Tool.** Use the `datacodex-notify` skill for every command, option, and exit code. The console program is `%s`; always call it by this full path.", cli)
	} else {
		line("**Tool.** The console program is `%s`. Always call it by this full path (it is not on PATH).", cli)
		line("Commands: `send --level info|warning|critical --text-file <file> --source %s --session <name> [--project <folder name>] [--title <text>] [--field key=value] [--mode text|voice|both] [--wait <seconds>]`, `connect --source %s --session <name> --project <folder name> --rules-file <absolute path> [--rules-file <absolute path>]`, `disconnect --source %s --session <name>`, `heartbeat --id <name> --every <seconds> --grace <seconds> --label \"<label>\" --source %s --session <name>`, `heartbeat-stop --id <name>`.", source, source, source, source)
		line("Always write the message to a UTF-8 file without a BOM and pass it with `--text-file`; never type Arabic directly on the command line.")
	}
	line("")

	line("### One-off message or task activation")
	line("There are two different cases. Never mix them up.")
	line("- **A one-off message the user asks for directly** (for example \"send me a test message\" or \"message me when this is done\"): run only `send` with `--wait 20`. Do **not** run `connect`, `disconnect`, or `heartbeat` for it. Add `--session <name>` only if a session is already active in this conversation. Never connect and disconnect around a single message.")
	line("- **Activating notifications for a task or a conversation**: `connect` **once**, as described below, then use the same session name for the whole conversation. Disconnect only when the user says a deactivation phrase or when the task ends.")
	line("")
	line("### Switching notifications on and off")
	line("Notifications are **OFF** for every session until they are activated.")
	switch o.Activation {
	case ActivationOnRequest:
		line("- Activate only when the user asks, for example with one of: %s.", quoteList(o.ActivatePhrases))
		line("- For a long task you may suggest once, in a single short line, that notifications can be turned on. Do not suggest again in the same session, and never turn them on yourself.")
	case ActivationSuggestLong:
		line("- Activate only when the user agrees or asks, for example with one of: %s.", quoteList(o.ActivatePhrases))
		line("- At the start of a long task, suggest notifications once, in a single short line. If the user does not answer, keep them off.")
	case ActivationAlways:
		line("- Notifications are on from the start of every manager session: activate them as soon as the session starts, without asking.")
		line("- The user can also activate them with one of: %s.", quoteList(o.ActivatePhrases))
	}
	line("- Deactivate when the user says one of: %s.", quoteList(o.DeactivatePhrases))
	line("")
	line("**To activate:**")
	line("1. Pick a unique session name once: the project name plus a short random suffix, for example `myproject-k7f2`. Reuse exactly this name for the whole conversation; never pick a new one.")
	line("2. Run `connect` with `--source %s`, that session name, `--project` with the project name (see \"The project name\" below), and `--rules-file` with the absolute path of every rules file that contains this block (the file you are reading now, and any other copy you know of).", source)
	line("3. If the program exits with code 6, notifications were turned off by the user: stop here and do not try again in this session.")
	if o.Heartbeat {
		line("4. Start the heartbeat (see below).")
	}
	line("")
	line("**To deactivate** (only on a deactivation phrase, or when the task ends): stop the heartbeat, run `disconnect` once with the same session name, and send nothing more in this session.")
	line("")

	line("### What to report")
	line("| Event | Level | Mode flag |")
	line("|-------|-------|-----------|")
	for _, event := range templateEvents[o.Template] {
		level := o.levelOf(event)
		line("| %s | %s | --mode %s |", eventText[event], level, o.modeOf(event))
	}
	line("")
	line("Do not report anything else. Always pass `--session` with your session name; the session remembers the project, so only `connect` needs `--project`. A one-off `send` without a session may pass `--project <folder name>` itself. Messages show the agent and the project in their header.")
	line("")

	line("### The project name")
	line("`--project` is the name of the git repository root folder of the work you are doing (the last part of `git rev-parse --show-toplevel`). If the work is not in a git repository, use the name of the working folder. If that name looks temporary (a temp or cache folder, a random or generated name, a pasted-files folder) or there is no real project, leave `--project` out.")
	line("")
	line("### Writing the messages")
	line("- Write every notification in %s.", languageText[o.Language])
	line("- Keep the body to one to three lines: what happened and what you need from the user, if anything.")
	line("- Use `--title` for a one-line summary (up to 120 characters) and `--field \"key=value\"` (repeatable, at most 8, key up to 40 and value up to 200 characters) for short facts such as the phase, the test result, or the step. Do not put formatting marks (`*`, `_`, `~`, backticks) in a title or a field; they are shown as plain text.")
	line("- Never include code, file contents, secrets, tokens, passwords, or customer data.")
	line("- Use `--source %s` on every command, the same value you gave to `connect`. The project is already in the session name.", source)
	line("")

	line("### Never let notifications get in the way")
	line("A failed notification must never fail, block, or delay your work.")
	line("- Exit 0: sent (or held on purpose). Exit 5: duplicate or rate limited; not an error.")
	line("- Exit 2: the app is not running. Continue the task and mention it once in your final report.")
	line("- Exit 3: authentication failed. Continue and mention it in your final report. Do not try to fix it.")
	line("- Exit 4: the command was invalid. Fix it and try once more.")
	line("- Exit 7: only with `--wait`: the message is still queued after the wait. Not an error; continue.")
	line("- Exit 6: the user turned notifications off. Stop sending notifications for the rest of this session and do not retry.")
	line("Do not change the app, its settings, or its files.")

	if o.Heartbeat {
		grace := o.HeartbeatEvery / 2
		line("")
		line("### Heartbeat")
		line("After activating, run `heartbeat --id <session name> --every %d --grace %d --label \"<project name>\" --source %s --session <session name>`.", o.HeartbeatEvery, grace, source)
		line("While you work, run `heartbeat --id <session name> --session <session name>` again at least every %d seconds (for example after every step). If you stop reporting in, the user is alerted.", o.HeartbeatEvery)
		line("When the task ends, or when you deactivate, run `heartbeat-stop --id <session name>`.")
	}

	if extra := strings.TrimSpace(o.Extra); extra != "" {
		line("")
		line("### Additional instructions from the user")
		line("%s", extra)
	}
	line("%s", EndMarker)
	return b.String(), nil
}

// fenceFor returns a code fence longer than any backtick run in text.
func fenceFor(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(4, longest+1))
}

// Connect returns the prompt the user gives to an agent so it writes the block
// into its own rules file.
func Connect(o Options, env Env) (string, error) {
	block, err := Block(o, env)
	if err != nil {
		return "", err
	}
	fence := fenceFor(block)

	var b strings.Builder
	line := func(format string, args ...interface{}) { fmt.Fprintf(&b, format+"\n", args...) }

	line("You are about to connect this agent to DataCodex Notify, a local app that sends WhatsApp notifications from long sessions. Do exactly the following, once, and do not do anything else.")
	line("")
	line("1. Open this rules file: %s.", rulesFileHint(o))
	line("2. Write the block below into that file. The block starts with the line `%s` and ends with the line `%s`. If the file already contains these markers, replace everything from the start marker to the end marker (both lines included) with the new block. Never add a second block, and do not change anything outside the markers.", StartMarker, EndMarker)
	if o.Activation == ActivationAlways {
		line("3. The activation mode is \"always on for manager sessions\": if you are the main (manager) session, now follow the block's \"To activate\" steps. A subagent, implementer, or delegate must not.")
	} else {
		line("3. Do not run any `datacodex-notify` command now. Notifications stay OFF until they are activated as the block describes.")
	}
	line("4. Reply with the absolute path of every file you changed, and nothing else.")
	line("")
	line("The block to write (copy it exactly, including the two marker lines):")
	line("")
	line("%smarkdown", fence)
	line("%s", block)
	line("%s", fence)
	return b.String(), nil
}

// SessionRef identifies a connected session for the disconnect prompt.
type SessionRef struct {
	Name       string
	Source     string
	RulesFiles []string
}

// DisconnectSession returns the prompt that removes one session: it deletes the
// marked block from the rules files recorded at connect time, then disconnects.
func DisconnectSession(ref SessionRef, env Env) string {
	var b strings.Builder
	line := func(format string, args ...interface{}) { fmt.Fprintf(&b, format+"\n", args...) }

	line("You are about to disconnect the agent session `%s` from DataCodex Notify. Do exactly the following and nothing else.", ref.Name)
	line("")
	if len(ref.RulesFiles) > 0 {
		line("1. In each of these rules files, delete only the block that starts with the line `%s` and ends with the line `%s` (both marker lines included). Leave everything else in the file unchanged. If a file does not contain the markers, leave it alone and say so in your reply.", StartMarker, EndMarker)
		for _, file := range ref.RulesFiles {
			line("   - `%s`", file)
		}
	} else {
		line("1. No rules file was recorded for this session. Look for the block that starts with the line `%s` and ends with the line `%s` in the usual rules files (see below), and delete only that block (both marker lines included).", StartMarker, EndMarker)
		for _, location := range genericLocations {
			line("   - %s", location)
		}
	}
	line("2. Stop any heartbeat you started for this session. The `disconnect` command in the next step also removes the session's heartbeats.")
	line("3. Run `%s disconnect --source %s --session %s`.", env.CLIPath, ref.Source, ref.Name)
	line("4. Do not send any more notifications in this session.")
	line("5. Reply with the list of files you changed and the exit code of the `disconnect` command.")
	return b.String()
}

var genericLocations = []string{
	"`CLAUDE.md` in the `.claude` folder of the user's home folder, and `CLAUDE.md` in the current project",
	"`AGENTS.md` in the `.codex` folder of the user's home folder, and `AGENTS.md` in the current project",
	"`GEMINI.md` in the `.gemini` folder of the user's home folder, and `GEMINI.md` in the current project",
	"any other rules file the user names",
}

// DisconnectGeneric returns a prompt that works without knowing the session: the
// agent searches the usual rules files for the block and removes it.
func DisconnectGeneric(env Env) string {
	var b strings.Builder
	line := func(format string, args ...interface{}) { fmt.Fprintf(&b, format+"\n", args...) }

	line("You are about to remove DataCodex Notify from this agent's rules. Do exactly the following and nothing else.")
	line("")
	line("1. Search these rules files for a block that starts with the line `%s` and ends with the line `%s`:", StartMarker, EndMarker)
	for _, location := range genericLocations {
		line("   - %s", location)
	}
	line("2. In every file where you find the block, delete only that block (both marker lines included). Leave everything else unchanged. Do not touch files without the markers.")
	line("3. Stop any heartbeat you started in this session.")
	line("4. If you connected this session earlier and know its session name and source, run `%s disconnect --source <source> --session <session name>`. If you do not know them, skip this step.", env.CLIPath)
	line("5. Do not send any more notifications in this session.")
	line("6. Reply with the list of files you changed (or say that you found no block).")
	return b.String()
}
