// Package inference talks to the local model server over its OpenAI-compatible
// HTTP API. Both backends expose the same API, so everything above this
// package works the same on every platform.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls a local inference server.
type Client struct {
	// BaseURL is the server root, such as http://127.0.0.1:11234.
	BaseURL string
	// HTTP performs the requests. A nil client uses a default one.
	HTTP *http.Client
}

// New returns a Client for a base URL.
func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{}}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Ready reports whether the server answers within the timeout.
func (c *Client) Ready(ctx context.Context, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/models", nil)
	if err != nil {
		return false
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// WaitReady polls until the server responds or the deadline passes.
func (c *Client) WaitReady(ctx context.Context, total time.Duration) error {
	deadline := time.Now().Add(total)
	for {
		if c.Ready(ctx, 2*time.Second) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("inference API at %s did not become ready within %s", c.BaseURL, total)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Models lists the model identifiers the server is serving.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/v1/models: %s", c.BaseURL, resp.Status)
	}
	var parsed modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// Serving reports whether the server currently offers a model whose id
// matches or contains the wanted name. Backends name models differently: MLX
// serves them by repository id, llama.cpp by file or alias.
func (c *Client) Serving(ctx context.Context, want string) (bool, error) {
	models, err := c.Models(ctx)
	if err != nil {
		return false, err
	}
	want = strings.ToLower(want)
	base := want
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".gguf")

	for _, m := range models {
		lower := strings.ToLower(m)
		if lower == want || strings.Contains(lower, base) {
			return true, nil
		}
		// The server may advertise a shorter alias than the reference MyAI
		// holds. Require enough of it to be meaningful, so a one or two
		// character id does not match everything.
		if len(lower) >= 4 && strings.Contains(base, lower) {
			return true, nil
		}
	}
	return false, nil
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			// Reasoning models put their working here and may leave Content
			// empty when they run out of tokens mid-thought.
			Reasoning string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Reply is what a model produced.
type Reply struct {
	// Content is the answer.
	Content string
	// Reasoning is the model's working, for models that expose it.
	Reasoning string
}

// Generated reports whether the model produced anything at all, which is what
// proves the path from request to tokens works.
func (r Reply) Generated() bool {
	return strings.TrimSpace(r.Content) != "" || strings.TrimSpace(r.Reasoning) != ""
}

// Complete sends a chat completion and returns the assistant's answer.
func (c *Client) Complete(ctx context.Context, model, prompt string, maxTokens int) (string, error) {
	reply, err := c.Ask(ctx, model, prompt, maxTokens)
	return reply.Content, err
}

// Ask sends a chat completion and returns everything the model produced.
func (c *Client) Ask(ctx context.Context, model, prompt string, maxTokens int) (Reply, error) {
	body, err := json.Marshal(chatRequest{
		Model:       model,
		Messages:    []chatMessage{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: 0,
	})
	if err != nil {
		return Reply{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http().Do(req)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Reply{}, err
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Reply{}, fmt.Errorf("unexpected reply from %s: %s", c.BaseURL, strings.TrimSpace(string(raw)))
	}
	if parsed.Error != nil {
		return Reply{}, fmt.Errorf("inference failed: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return Reply{}, fmt.Errorf("inference failed: %s", resp.Status)
	}
	if len(parsed.Choices) == 0 {
		return Reply{}, fmt.Errorf("inference returned no choices")
	}
	return Reply{
		Content:   parsed.Choices[0].Message.Content,
		Reasoning: parsed.Choices[0].Message.Reasoning,
	}, nil
}

// Warm loads the model into memory by asking for a single token. It is how
// MyAI honours the keep-model-in-RAM setting on both backends.
func (c *Client) Warm(ctx context.Context, model string) error {
	_, err := c.Complete(ctx, model, "hi", 1)
	return err
}

// contextKeys are the field names servers use for a model's context window.
// Different OpenAI-compatible servers name it differently, and some do not
// report it at all.
var contextKeys = []string{
	"context_length",
	"max_context_length",
	"context_window",
	"max_model_len",
	"n_ctx",
}

// ContextLength reports the context window the server advertises for a model,
// and whether it said anything at all. MyAI tells OpenCode how much context a
// model has, so it is worth knowing when the server disagrees.
func (c *Client) ContextLength(ctx context.Context, model string) (int, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/models", nil)
	if err != nil {
		return 0, false
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}

	var parsed struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, false
	}

	want := strings.ToLower(model)
	for _, entry := range parsed.Data {
		id, _ := entry["id"].(string)
		if len(parsed.Data) > 1 && !strings.EqualFold(id, model) && !strings.Contains(strings.ToLower(id), want) {
			continue
		}
		if n, ok := findContext(entry); ok {
			return n, true
		}
	}
	return 0, false
}

// findContext looks for a context window in a model entry, including one
// nested a level down.
func findContext(entry map[string]any) (int, bool) {
	for _, key := range contextKeys {
		if v, ok := entry[key]; ok {
			if n, ok := toInt(v); ok && n > 0 {
				return n, true
			}
		}
	}
	for _, v := range entry {
		nested, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range contextKeys {
			if raw, ok := nested[key]; ok {
				if n, ok := toInt(raw); ok && n > 0 {
					return n, true
				}
			}
		}
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}
