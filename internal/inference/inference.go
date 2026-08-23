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
		if lower == want || strings.Contains(lower, base) || strings.Contains(base, lower) {
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
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends a chat completion and returns the assistant's reply.
func (c *Client) Complete(ctx context.Context, model, prompt string, maxTokens int) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:       model,
		Messages:    []chatMessage{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("unexpected reply from %s: %s", c.BaseURL, strings.TrimSpace(string(raw)))
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("inference failed: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inference failed: %s", resp.Status)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("inference returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// Warm loads the model into memory by asking for a single token. It is how
// MyAI honours the keep-model-in-RAM setting on both backends.
func (c *Client) Warm(ctx context.Context, model string) error {
	_, err := c.Complete(ctx, model, "hi", 1)
	return err
}
