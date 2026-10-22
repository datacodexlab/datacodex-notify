// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"flag"
	"fmt"
	"net/url"
)

// heartbeatRequest mirrors the POST /heartbeats/{id} body. Grace is a pointer so
// that "not given" differs from zero.
type heartbeatRequest struct {
	Label   string `json:"label,omitempty"`
	Source  string `json:"source,omitempty"`
	Session string `json:"session,omitempty"`
	Every   int    `json:"every_s,omitempty"`
	Grace   *int   `json:"grace_s,omitempty"`
}

func runHeartbeat(args []string, e env) int {
	fs := flag.NewFlagSet("heartbeat", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	id := fs.String("id", "", "heartbeat id, for example the run or plan name")
	every := fs.Int("every", 0, "expected seconds between beats (default from the app settings)")
	grace := fs.Int("grace", -1, "extra seconds to wait before alerting (default from the app settings)")
	label := fs.String("label", "", "human-readable label shown in the alert")
	source := fs.String("source", "", "source agent and project")
	session := fs.String("session", "", "the agent session name used with connect")

	if err := fs.Parse(args); err != nil {
		return exitValidation
	}
	if *id == "" {
		return fail(e, exitValidation, fmt.Errorf("--id is required"))
	}
	if *every < 0 {
		return fail(e, exitValidation, fmt.Errorf("--every must be a positive number of seconds"))
	}

	req := heartbeatRequest{Label: *label, Source: *source, Session: *session, Every: *every}
	if *grace >= 0 {
		req.Grace = grace
	}

	client, code, err := e.newClient()
	if err != nil {
		return fail(e, code, err)
	}
	var resp map[string]interface{}
	if code, err := client.Post("/heartbeats/"+url.PathEscape(*id), req, &resp); err != nil {
		return fail(e, code, err)
	}
	return printJSON(e, resp)
}

func runHeartbeatStop(args []string, e env) int {
	fs := flag.NewFlagSet("heartbeat-stop", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	id := fs.String("id", "", "heartbeat id to stop")

	if err := fs.Parse(args); err != nil {
		return exitValidation
	}
	if *id == "" {
		return fail(e, exitValidation, fmt.Errorf("--id is required"))
	}

	client, code, err := e.newClient()
	if err != nil {
		return fail(e, code, err)
	}
	var resp map[string]interface{}
	if code, err := client.Delete("/heartbeats/"+url.PathEscape(*id), &resp); err != nil {
		return fail(e, code, err)
	}
	return printJSON(e, resp)
}
