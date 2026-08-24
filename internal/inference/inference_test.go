package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func modelsHandler(ids ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
}

func TestReady(t *testing.T) {
	c := fakeServer(t, modelsHandler("mlx-community/Qwen3.5-9B-6bit"))
	if !c.Ready(context.Background(), time.Second) {
		t.Error("server should be ready")
	}
}

func TestReadyFalseWhenUnreachable(t *testing.T) {
	c := New("http://127.0.0.1:1")
	if c.Ready(context.Background(), 500*time.Millisecond) {
		t.Error("nothing is listening; Ready should be false")
	}
}

func TestModels(t *testing.T) {
	c := fakeServer(t, modelsHandler("a/b", "c/d"))
	got, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a/b" {
		t.Errorf("Models = %v", got)
	}
}

func TestServingMatchesAcrossBackendNamingStyles(t *testing.T) {
	tests := []struct {
		served []string
		want   string
		ok     bool
	}{
		// mlx-serve reports the repository id.
		{[]string{"mlx-community/Qwen3.5-9B-6bit"}, "mlx-community/Qwen3.5-9B-6bit", true},
		// llama-server reports the file or a short alias.
		{[]string{"Qwen3.5-9B-Q6_K.gguf"}, "unsloth/Qwen3.5-9B-GGUF/Qwen3.5-9B-Q6_K.gguf", true},
		{[]string{"Qwen3.5-9B-Q6_K"}, "Qwen3.5-9B-Q6_K.gguf", true},
		{[]string{"some-other-model"}, "mlx-community/Qwen3.5-9B-6bit", false},
	}
	for _, tt := range tests {
		c := fakeServer(t, modelsHandler(tt.served...))
		got, err := c.Serving(context.Background(), tt.want)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.ok {
			t.Errorf("Serving(%q) with %v = %v, want %v", tt.want, tt.served, got, tt.ok)
		}
	}
}

func TestComplete(t *testing.T) {
	c := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "test-model" {
			t.Errorf("model = %v", req["model"])
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "myai-ok"}},
			},
		})
	})

	got, err := c.Complete(context.Background(), "test-model", "Reply exactly: myai-ok", 16)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != "myai-ok" {
		t.Errorf("Complete = %q", got)
	}
}

func TestCompleteSurfacesServerError(t *testing.T) {
	c := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "model not found"},
		})
	})

	_, err := c.Complete(context.Background(), "missing", "hi", 1)
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Errorf("err = %v, want the server's own message", err)
	}
}

func TestCompleteRejectsNonJSON(t *testing.T) {
	c := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>gateway error</html>"))
	})
	if _, err := c.Complete(context.Background(), "m", "hi", 1); err == nil {
		t.Error("expected an error for a non-JSON reply")
	}
}

func TestWaitReadyGivesUp(t *testing.T) {
	c := New("http://127.0.0.1:1")
	err := c.WaitReady(context.Background(), 1200*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("err = %v", err)
	}
}

func TestWaitReadySucceedsOnceServerAnswers(t *testing.T) {
	c := fakeServer(t, modelsHandler("a/b"))
	if err := c.WaitReady(context.Background(), 3*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWarmAsksForOneToken(t *testing.T) {
	var maxTokens float64
	c := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		maxTokens, _ = req["max_tokens"].(float64)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "."}}},
		})
	})

	if err := c.Warm(context.Background(), "test-model"); err != nil {
		t.Fatal(err)
	}
	if maxTokens != 1 {
		t.Errorf("max_tokens = %v, want 1", maxTokens)
	}
}

func TestServingIgnoresTrivialSubstringMatches(t *testing.T) {
	// A one or two character id must not match every reference.
	c := fakeServer(t, modelsHandler("x", "ab"))
	got, err := c.Serving(context.Background(), "mlx-community/Qwen3.5-9B-6bit")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("a trivial id should not count as serving the model")
	}
}
