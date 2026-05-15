package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TelegramAlerter sends error alerts to a Telegram chat.
type TelegramAlerter struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegramAlerter creates a new alerter. Returns nil if not configured.
func NewTelegramAlerter(botToken, chatID string) *TelegramAlerter {
	if botToken == "" || chatID == "" {
		return nil
	}
	return &TelegramAlerter{
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// ErrorDetails holds the context of a 5xx error.
type ErrorDetails struct {
	RequestID  string
	Method     string
	Path       string
	StatusCode int
	ClientIP   string
	UserAgent  string
	Latency    time.Duration
	Error      string
}

// SendAlert sends a formatted error alert to Telegram.
func (t *TelegramAlerter) SendAlert(d ErrorDetails) {
	text := fmt.Sprintf(
		"🚨 *Production Error*\n\n"+
			"*Status:* `%d`\n"+
			"*Method:* `%s`\n"+
			"*Path:* `%s`\n"+
			"*Request ID:* `%s`\n"+
			"*Latency:* `%s`\n"+
			"*Client IP:* `%s`\n"+
			"*User Agent:* `%s`\n"+
			"*Time:* `%s`",
		d.StatusCode, d.Method, d.Path, d.RequestID,
		d.Latency.String(), d.ClientIP, d.UserAgent,
		time.Now().UTC().Format(time.RFC3339),
	)
	if d.Error != "" {
		text += fmt.Sprintf("\n*Error:* `%s`", d.Error)
	}

	t.send(text)
}

// SendFatal sends an alert for a fatal startup/shutdown crash.
func (t *TelegramAlerter) SendFatal(msg string, err error) {
	text := fmt.Sprintf(
		"💀 *Server Crash*\n\n"+
			"*Message:* `%s`\n"+
			"*Error:* `%s`\n"+
			"*Time:* `%s`",
		msg, err.Error(), time.Now().UTC().Format(time.RFC3339),
	)
	t.send(text)
}

func (t *TelegramAlerter) send(text string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return
	}
	resp.Body.Close()
}

// SendSweepExhausted alerts when a deposit sweep has exhausted all retry attempts.
func (t *TelegramAlerter) SendSweepExhausted(sweepID, depositID, sourceChain, amount string, attempts int) {
	if t == nil {
		return
	}
	text := fmt.Sprintf(
		"⚠️ *Deposit Sweep Exhausted*\n\n"+
			"*Sweep ID:* `%s`\n"+
			"*Deposit ID:* `%s`\n"+
			"*Source Chain:* `%s`\n"+
			"*Amount:* `%s USDC`\n"+
			"*Attempts:* `%d/%d`\n"+
			"*Time:* `%s`\n\n"+
			"Manual intervention required — funds remain on source chain.",
		sweepID, depositID, sourceChain, amount, attempts, 5,
		time.Now().UTC().Format(time.RFC3339),
	)
	t.send(text)
}
