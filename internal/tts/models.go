// SPDX-License-Identifier: GPL-3.0-only

package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// ModelInfo describes one selectable TTS model.
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type modelsPage struct {
	Models []struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"models"`
	NextPageToken string `json:"nextPageToken"`
}

// maxModelPages bounds paging so a misbehaving server cannot loop forever.
const maxModelPages = 20

// ListModels asks Google for the models available to the first usable key and
// keeps only text-to-speech models (names containing "tts").
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	keys, err := c.keys.Keys(ctx)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, ErrNoKeys
	}

	var lastErr error
	for _, key := range keys {
		models, apiErr := c.listModels(ctx, key)
		if apiErr == nil {
			return models, nil
		}
		lastErr = apiErr
		if apiErr.Kind != KindInvalidKey && apiErr.Kind != KindRateLimit {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) listModels(ctx context.Context, key Key) ([]ModelInfo, *Error) {
	var found []ModelInfo
	token := ""
	for page := 0; page < maxModelPages; page++ {
		path := "models?pageSize=100"
		if token != "" {
			path += "&pageToken=" + url.QueryEscape(token)
		}
		data, apiErr := c.do(ctx, http.MethodGet, key, path, nil)
		if apiErr != nil {
			return nil, apiErr
		}

		var parsed modelsPage
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, &Error{Kind: KindOther, Message: "the model list is not valid JSON"}
		}
		for _, m := range parsed.Models {
			id := strings.TrimPrefix(m.Name, "models/")
			if !strings.Contains(strings.ToLower(id), "tts") {
				continue
			}
			name := m.DisplayName
			if name == "" {
				name = id
			}
			found = append(found, ModelInfo{ID: id, DisplayName: name})
		}
		if parsed.NextPageToken == "" {
			break
		}
		token = parsed.NextPageToken
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	return found, nil
}

// TestModel checks a model with a real minimal speech generation using the first
// usable key. A 200 or a 429 accepts the model; anything else is reported with
// Google's message.
func (c *Client) TestModel(ctx context.Context, model string) KeyTestResult {
	keys, err := c.keys.Keys(ctx)
	if err != nil {
		return KeyTestResult{Message: err.Error()}
	}
	if len(keys) == 0 {
		return KeyTestResult{Message: ErrNoKeys.Error()}
	}

	result := KeyTestResult{}
	for _, key := range keys {
		_, apiErr := c.generateSpeech(ctx, key, model, "ok", DefaultVoice)
		switch {
		case apiErr == nil:
			return KeyTestResult{Valid: true}
		case apiErr.Kind == KindRateLimit:
			return KeyTestResult{Valid: true, RateLimited: true, Message: apiErr.Message}
		case apiErr.Kind == KindInvalidKey:
			result = KeyTestResult{Message: apiErr.Message}
		default:
			return KeyTestResult{Message: apiErr.Message}
		}
	}
	return result
}
