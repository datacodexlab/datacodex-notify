// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const usage = `Usage: datacodex-notify <command> [options]
Commands:
  send            Send a notification to WhatsApp
  connect         Register this agent session and its rules files
  disconnect      Remove this agent session
  heartbeat       Register or refresh a dead-man's-switch heartbeat
  heartbeat-stop  Stop monitoring a heartbeat
  skill           Install, remove, or check the agent skill (install | remove | status)
  status          Check service and connection status
  open            Open the admin UI window
  version         Display application version`

// env holds the process-level dependencies of a command so tests can replace them.
type env struct {
	stdout    io.Writer
	stderr    io.Writer
	newClient func() (*Client, int, error)
}

// Run executes a CLI command and returns the process exit code.
func Run(args []string, version string) int {
	attachParentConsole()
	return execute(args, version, env{stdout: os.Stdout, stderr: os.Stderr, newClient: NewClient})
}

func execute(args []string, version string, e env) int {
	if len(args) == 0 {
		fmt.Fprintln(e.stdout, usage)
		return exitOK
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(e.stdout, "DataCodex Notify v%s\n", version)
		return exitOK
	case "status":
		return runStatus(e)
	case "open":
		return runOpen(e)
	case "send":
		return runSend(args[1:], e)
	case "connect":
		return runConnect(args[1:], e)
	case "disconnect":
		return runDisconnect(args[1:], e)
	case "heartbeat":
		return runHeartbeat(args[1:], e)
	case "heartbeat-stop":
		return runHeartbeatStop(args[1:], e)
	case "skill":
		return runSkill(args[1:], e)
	case "help", "-h", "--help":
		fmt.Fprintln(e.stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(e.stderr, "unknown command: %s\n%s\n", args[0], usage)
		return exitValidation
	}
}

func runStatus(e env) int {
	client, code, err := e.newClient()
	if err != nil {
		return fail(e, code, err)
	}
	var resp map[string]interface{}
	if code, err := client.Get("/status", &resp); err != nil {
		return fail(e, code, err)
	}
	return printJSON(e, resp)
}

func runOpen(e env) int {
	client, code, err := e.newClient()
	if err != nil {
		return fail(e, code, err)
	}
	if code, err := client.Post("/ui/open", nil, nil); err != nil {
		return fail(e, code, err)
	}
	return printJSON(e, map[string]bool{"ok": true})
}

// maxSendWait is the longest --wait the service accepts, in seconds.
const maxSendWait = 60

// sendRequest mirrors the POST /notify body.
type sendRequest struct {
	Level     string   `json:"level"`
	Text      string   `json:"text"`
	Speech    string   `json:"speech,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	To        []string `json:"to,omitempty"`
	Source    string   `json:"source,omitempty"`
	DedupeKey string   `json:"dedupe_key,omitempty"`
	Session   string   `json:"session,omitempty"`
	Wait      int      `json:"wait,omitempty"`
	Project   string   `json:"project,omitempty"`
	Title     string   `json:"title,omitempty"`
	Fields    []field  `json:"fields,omitempty"`
}

// field is one key and value shown under the message body.
type field struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Limits the service enforces; checked here too so the agent gets a clear exit 4.
const (
	maxTitleRunes = 120
	maxFields     = 8
	maxKeyRunes   = 40
	maxValueRunes = 200
)

// parseFields turns repeated key=value flags into fields.
func parseFields(raw []string) ([]field, error) {
	if len(raw) > maxFields {
		return nil, fmt.Errorf("at most %d --field options are allowed", maxFields)
	}
	fields := make([]field, 0, len(raw))
	for _, item := range raw {
		key, value, ok := strings.Cut(item, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("--field must look like key=value, got %q", item)
		}
		if utf8.RuneCountInString(key) > maxKeyRunes || utf8.RuneCountInString(value) > maxValueRunes {
			return nil, fmt.Errorf("--field keys are limited to %d characters and values to %d", maxKeyRunes, maxValueRunes)
		}
		fields = append(fields, field{Key: key, Value: value})
	}
	return fields, nil
}

func runSend(args []string, e env) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	level := fs.String("level", "", "notification level: info, warning, critical")
	text := fs.String("text", "", "message text")
	textFile := fs.String("text-file", "", "UTF-8 file with the message text")
	speech := fs.String("speech", "", "shorter text to speak")
	speechFile := fs.String("speech-file", "", "UTF-8 file with the speech text")
	mode := fs.String("mode", "auto", "delivery mode: auto, text, voice, both")
	to := fs.String("to", "", "recipient name or phone number")
	source := fs.String("source", "", "source agent and project")
	dedupeKey := fs.String("dedupe-key", "", "deduplication key")
	session := fs.String("session", "", "the agent session name used with connect")
	project := fs.String("project", "", "the project folder name; a session already carries it")
	title := fs.String("title", "", "one-line summary shown above the message body (max 120 characters)")
	var rawFields stringList
	fs.Var(&rawFields, "field", "a short fact as key=value, shown under the body (repeatable, max 8)")
	wait := fs.Int("wait", 0, "wait up to N seconds (max 60) until the message is sent")

	if err := fs.Parse(args); err != nil {
		return exitValidation
	}
	if *level == "" {
		return fail(e, exitValidation, fmt.Errorf("--level is required (info, warning, critical)"))
	}

	if *wait < 0 || *wait > maxSendWait {
		return fail(e, exitValidation, fmt.Errorf("--wait must be between 0 and %d seconds", maxSendWait))
	}

	if utf8.RuneCountInString(*title) > maxTitleRunes {
		return fail(e, exitValidation, fmt.Errorf("--title is limited to %d characters", maxTitleRunes))
	}
	fields, err := parseFields(rawFields)
	if err != nil {
		return fail(e, exitValidation, err)
	}

	finalText, err := readTextOrFile(*text, *textFile)
	if err != nil {
		return fail(e, exitValidation, err)
	}
	if finalText == "" {
		return fail(e, exitValidation, fmt.Errorf("--text or --text-file is required"))
	}
	finalSpeech, err := readTextOrFile(*speech, *speechFile)
	if err != nil {
		return fail(e, exitValidation, err)
	}

	req := sendRequest{
		Level:     *level,
		Text:      finalText,
		Speech:    finalSpeech,
		Mode:      *mode,
		Source:    *source,
		DedupeKey: *dedupeKey,
		Session:   *session,
		Wait:      *wait,
		Project:   *project,
		Title:     strings.TrimSpace(*title),
		Fields:    fields,
	}
	if *to != "" {
		req.To = []string{*to}
	}

	client, code, err := e.newClient()
	if err != nil {
		return fail(e, code, err)
	}
	if *wait > 0 && client.HTTPClient != nil {
		// The reply comes after the wait, so the request must outlive it.
		client.HTTPClient.Timeout = time.Duration(*wait)*time.Second + requestTimeout
	}
	var resp map[string]interface{}
	if code, err := client.Post("/notify", req, &resp); err != nil {
		return fail(e, code, err)
	}
	code = printJSON(e, resp)
	if code != exitOK {
		return code
	}
	if status, _ := resp["status"].(string); status == "deduplicated" || status == "rate_limited" {
		return exitThrottled
	}
	if *wait > 0 && resp["status"] == "queued" {
		return exitStillQueued
	}
	if *wait > 0 && resp["status"] == "failed" {
		return exitServerError
	}
	return exitOK
}

func printJSON(e env, v interface{}) int {
	data, err := json.Marshal(v)
	if err != nil {
		return fail(e, exitServerError, err)
	}
	fmt.Fprintln(e.stdout, string(data))
	return exitOK
}

func fail(e env, code int, err error) int {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintln(e.stderr, string(data))
	return code
}
