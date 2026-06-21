package ai

import "encoding/json"

// estimateTokens returns a rough token count for a string (~4 chars per token).
func estimateTokens(s string) int {
	return len(s)/4 + 1
}

// estimateMessagesTokens estimates total tokens for a slice of messages.
func estimateMessagesTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += estimateTokens(m.Content) + 4 // message overhead
		if m.ReasoningContent != "" {
			total += estimateTokens(m.ReasoningContent)
		}
		for _, tc := range m.ToolCalls {
			total += estimateTokens(tc.Name) + 10
			if tc.Arguments != nil {
				b, _ := json.Marshal(tc.Arguments)
				total += estimateTokens(string(b))
			}
		}
	}
	return total
}

// estimateToolsTokens estimates tokens consumed by tool definitions.
func estimateToolsTokens(tools []Tool) int {
	if len(tools) == 0 {
		return 0
	}
	b, _ := json.Marshal(tools)
	return estimateTokens(string(b))
}

// truncateMessages trims older conversation messages to fit within maxTokens.
// It preserves system messages and the last user message, removing middle
// history messages until the estimate fits.
func truncateMessages(messages []Message, systemPrompt string, tools []Tool, maxTokens int) []Message {
	if maxTokens <= 0 {
		return messages
	}

	overhead := estimateTokens(systemPrompt) + estimateToolsTokens(tools) + 256 // response buffer
	budget := maxTokens - overhead
	if budget <= 0 {
		// Can't fit anything; keep only the last message
		if len(messages) > 0 {
			return messages[len(messages)-1:]
		}
		return messages
	}

	total := estimateMessagesTokens(messages)
	if total <= budget {
		return messages
	}

	// Separate: leading system messages, middle (history), tail (last 2 messages)
	var head []Message // system messages at start
	var tail []Message // last 2 messages (latest context + user msg)

	headEnd := 0
	for headEnd < len(messages) && messages[headEnd].Role == "system" {
		headEnd++
	}
	head = messages[:headEnd]

	tailStart := len(messages) - 2
	if tailStart < headEnd {
		tailStart = headEnd
	}
	tail = messages[tailStart:]
	middle := messages[headEnd:tailStart]

	// Calculate fixed tokens
	fixed := estimateMessagesTokens(head) + estimateMessagesTokens(tail)
	remaining := budget - fixed
	if remaining <= 0 {
		return append(head, tail...)
	}

	// Keep as many recent middle messages as fit
	kept := make([]Message, 0, len(middle))
	for i := len(middle) - 1; i >= 0; i-- {
		cost := estimateTokens(middle[i].Content) + 4
		if remaining-cost < 0 {
			break
		}
		remaining -= cost
		kept = append([]Message{middle[i]}, kept...)
	}

	result := make([]Message, 0, len(head)+len(kept)+len(tail))
	result = append(result, head...)
	result = append(result, kept...)
	result = append(result, tail...)
	return result
}
