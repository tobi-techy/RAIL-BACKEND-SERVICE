package ai

import "strings"

// emitWithBubbleBreaks splits content on paragraph breaks and emits bubble_break events.
// Recognizes both \n\n (standard) and single \n (common in LLM output) as bubble boundaries.
func emitWithBubbleBreaks(content string, emit func(StreamEvent)) {
	// First try \n\n (intentional paragraph breaks)
	parts := strings.Split(content, "\n\n")
	if len(parts) > 1 {
		for i, part := range parts {
			if i > 0 {
				emit(StreamEvent{Type: "bubble_break"})
			}
			if part = strings.TrimSpace(part); part != "" {
				emit(StreamEvent{Type: "token", Content: part})
			}
		}
		return
	}

	// Fallback: split on single \n if the response has them
	parts = strings.Split(content, "\n")
	if len(parts) > 1 {
		for i, part := range parts {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			if i > 0 {
				emit(StreamEvent{Type: "bubble_break"})
			}
			emit(StreamEvent{Type: "token", Content: part})
		}
		return
	}

	// No breaks at all — emit as single bubble
	emit(StreamEvent{Type: "token", Content: content})
}

// bubbleBreakBuffer buffers streaming tokens and emits bubble_break on \n\n boundaries.
type bubbleBreakBuffer struct {
	emit func(StreamEvent)
	buf  strings.Builder
}

// Write processes incoming token text, emitting tokens and bubble_break events.
func (b *bubbleBreakBuffer) Write(s string) {
	b.buf.WriteString(s)
	for {
		text := b.buf.String()
		// Prefer \n\n, fall back to \n
		idx := strings.Index(text, "\n\n")
		skip := 2
		if idx < 0 {
			idx = strings.Index(text, "\n")
			skip = 1
		}
		if idx < 0 {
			break
		}
		if idx > 0 {
			b.emit(StreamEvent{Type: "token", Content: strings.TrimSpace(text[:idx])})
		}
		b.emit(StreamEvent{Type: "bubble_break"})
		b.buf.Reset()
		b.buf.WriteString(text[idx+skip:])
	}
}

// Flush emits any remaining buffered content.
func (b *bubbleBreakBuffer) Flush() {
	if b.buf.Len() > 0 {
		b.emit(StreamEvent{Type: "token", Content: b.buf.String()})
		b.buf.Reset()
	}
}
