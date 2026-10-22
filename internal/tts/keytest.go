// SPDX-License-Identifier: GPL-3.0-only

package tts

import (
	"context"
	"encoding/json"
	"net/http"
)

// KeyTestResult is the outcome of a one-token generation with a key.
type KeyTestResult struct {
	// Valid is true when Google accepted the key (including when it answered with a rate limit).
	Valid       bool
	RateLimited bool
	// Message is Google's own text when the key is not valid or is rate limited.
	Message string
}

type textRequest struct {
	Contents         []textContent    `json:"contents"`
	GenerationConfig textGenerationOp `json:"generationConfig"`
}

type textContent struct {
	Parts []textPart `json:"parts"`
}

type textPart struct {
	Text string `json:"text"`
}

type textGenerationOp struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
}

// TestKey checks a key with a real minimal text generation. A 200 means the
// key is valid, a 429 means it is valid but rate limited, anything else is
// reported with Google's message.
func (c *Client) TestKey(ctx context.Context, key Key) KeyTestResult {
	body, err := json.Marshal(textRequest{
		Contents:         []textContent{{Parts: []textPart{{Text: "ping"}}}},
		GenerationConfig: textGenerationOp{MaxOutputTokens: 1},
	})
	if err != nil {
		return KeyTestResult{Message: "failed to encode the request"}
	}

	_, apiErr := c.do(ctx, http.MethodPost, key, "models/"+ModelText+":generateContent", body)
	switch {
	case apiErr == nil:
		return KeyTestResult{Valid: true}
	case apiErr.Kind == KindRateLimit:
		return KeyTestResult{Valid: true, RateLimited: true, Message: apiErr.Message}
	default:
		return KeyTestResult{Message: apiErr.Message}
	}
}
