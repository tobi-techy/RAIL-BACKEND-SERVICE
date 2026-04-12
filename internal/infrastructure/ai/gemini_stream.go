package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const geminiStreamURLTemplate = "https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s"

// ChatCompletionStream streams chat completion chunks via a channel.
func (p *GeminiProvider) ChatCompletionStream(ctx context.Context, req *ChatRequest, tools []Tool, ch chan<- StreamChunk) error {
	defer close(ch)

	if err := p.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit: %w", err)
	}

	geminiReq := p.buildGeminiRequest(req, tools)
	reqBody, err := json.Marshal(geminiReq)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	apiURL := fmt.Sprintf(geminiStreamURLTemplate, p.config.Model, p.config.APIKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return p.handleHTTPError(resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	var totalTokens int

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var chunk geminiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.UsageMetadata != nil {
			totalTokens = chunk.UsageMetadata.TotalTokenCount
		}

		if len(chunk.Candidates) == 0 {
			continue
		}

		candidate := chunk.Candidates[0]
		sc := StreamChunk{}

		for _, part := range candidate.Content.Parts {
			if text, ok := part["text"].(string); ok {
				sc.Content += text
			}
			if funcCall, ok := part["functionCall"].(map[string]interface{}); ok {
				name, _ := funcCall["name"].(string)
				if name == "" {
					continue
				}
				tc := ToolCall{
					ID:   fmt.Sprintf("call_%d", len(sc.ToolCalls)),
					Name: name,
				}
				if args, ok := funcCall["args"].(map[string]interface{}); ok {
					tc.Arguments = args
				}
				sc.ToolCalls = append(sc.ToolCalls, tc)
			}
		}

		if candidate.FinishReason == "STOP" || candidate.FinishReason == "MAX_TOKENS" {
			sc.Done = true
			sc.TokensUsed = totalTokens
		}

		select {
		case ch <- sc:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return scanner.Err()
}
