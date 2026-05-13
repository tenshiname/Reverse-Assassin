package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"reverse-assassin/internal/config"
)

type Client struct {
	httpClient *http.Client
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

func (c *Client) key() string   { return config.LLMAPIKey() }
func (c *Client) base() string  { return config.LLMBaseURL() }
func (c *Client) model() string { return config.LLMModel() }

// Chat sends a chat request with context support for cancellation/timeout.
func (c *Client) Chat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	req := ChatRequest{
		Model: c.model(),
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
		MaxTokens: 4096,
		Stream:    false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(c.base(), "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.key())

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if chatResp.Error != nil {
		return "", fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("empty response from LLM")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// ChatJSON sends a request and unmarshals the JSON response into target.
func (c *Client) ChatJSON(ctx context.Context, systemPrompt, userMessage string, target interface{}) error {
	text, err := c.Chat(ctx, systemPrompt, userMessage)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(text), target); err != nil {
		text = extractJSON(text)
		if err2 := json.Unmarshal([]byte(text), target); err2 != nil {
			return fmt.Errorf("unmarshal LLM JSON response: %w\nRaw: %s", err, text)
		}
	}
	return nil
}
