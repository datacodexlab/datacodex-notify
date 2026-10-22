// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
)

// stringList is a flag that may be given several times.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

type connectRequest struct {
	Source     string   `json:"source"`
	Session    string   `json:"session"`
	Project    string   `json:"project,omitempty"`
	RulesFiles []string `json:"rules_files"`
}

func runConnect(args []string, e env) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	source := fs.String("source", "", "agent type: claude-code, codex, gemini, or other")
	session := fs.String("session", "", "a unique name for this agent session")
	project := fs.String("project", "", "the project folder name, shown in the message header")
	var rulesFiles stringList
	fs.Var(&rulesFiles, "rules-file", "absolute path of a rules file the agent wrote the notification block into (repeatable)")

	if err := fs.Parse(args); err != nil {
		return exitValidation
	}
	if *source == "" || *session == "" {
		return fail(e, exitValidation, fmt.Errorf("--source and --session are required"))
	}

	files := make([]string, 0, len(rulesFiles))
	for _, file := range rulesFiles {
		abs, err := filepath.Abs(file)
		if err != nil {
			return fail(e, exitValidation, fmt.Errorf("invalid --rules-file %q: %w", file, err))
		}
		files = append(files, abs)
	}

	client, code, err := e.newClient()
	if err != nil {
		return fail(e, code, err)
	}
	var resp map[string]interface{}
	if code, err := client.Post("/agents/connect", connectRequest{Source: *source, Session: *session, Project: *project, RulesFiles: files}, &resp); err != nil {
		return fail(e, code, err)
	}
	return printJSON(e, resp)
}

func runDisconnect(args []string, e env) int {
	fs := flag.NewFlagSet("disconnect", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	// --source is accepted so the connect and disconnect commands read the same;
	// the session name alone identifies the session.
	fs.String("source", "", "agent type (optional)")
	session := fs.String("session", "", "the session name used with connect")

	if err := fs.Parse(args); err != nil {
		return exitValidation
	}
	if *session == "" {
		return fail(e, exitValidation, fmt.Errorf("--session is required"))
	}

	client, code, err := e.newClient()
	if err != nil {
		return fail(e, code, err)
	}
	var resp map[string]interface{}
	if code, err := client.Post("/agents/disconnect", map[string]string{"session": *session}, &resp); err != nil {
		return fail(e, code, err)
	}
	return printJSON(e, resp)
}
