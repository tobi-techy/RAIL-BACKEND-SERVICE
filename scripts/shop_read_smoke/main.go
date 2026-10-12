// Command shop_read_smoke runs one shop read against a live Python reader.
//
// It drives the real client the platform uses — same per-user token minting,
// same request body, same decoding of the answer — so it is the end-to-end
// check of the Go <-> reader contract that the unit tests fake out. The
// processor's routing, holding bubble and dedupe are covered separately by
// internal/infrastructure/platform/shop_documents_test.go.
//
//	PYTHON_AGENT_URL=http://127.0.0.1:8099 \
//	JWT_SECRET=<the reader's JWT secret> \
//	SHOP_READ_USER_ID=<rail user uuid> \
//	go run ./scripts/shop_read_smoke -file page.jpg [-hint stock_book] [-caption "stock book"]
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

func main() {
	file := flag.String("file", "", "path to the page (image or PDF)")
	hint := flag.String("hint", "", "doc hint: stock_book | invoice | delivery_note")
	caption := flag.String("caption", "", "caption, as if the owner typed it")
	msgID := flag.String("msg-id", "", "bridge message id (defaults to a timestamp)")
	flag.Parse()

	if strings.TrimSpace(*file) == "" {
		log.Fatal("-file is required")
	}
	baseURL := strings.TrimSpace(os.Getenv("PYTHON_AGENT_URL"))
	if baseURL == "" {
		log.Fatal("PYTHON_AGENT_URL is required (e.g. http://127.0.0.1:8099)")
	}
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET is required and must match the reader's")
	}
	rawUserID := strings.TrimSpace(os.Getenv("SHOP_READ_USER_ID"))
	if rawUserID == "" {
		log.Fatal("SHOP_READ_USER_ID is required (the owner the document is stored for)")
	}
	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		log.Fatalf("SHOP_READ_USER_ID is not a uuid: %v", err)
	}

	data, err := os.ReadFile(*file)
	if err != nil {
		log.Fatalf("read %s: %v", *file, err)
	}
	id := *msgID
	if id == "" {
		id = fmt.Sprintf("smoke-%d", time.Now().Unix())
	}

	client := ai.NewPythonAgentClient(ai.PythonAgentClientConfig{
		BaseURL:         baseURL,
		JWTSecret:       jwtSecret,
		JWTTTL:          120 * time.Second,
		ShopReadTimeout: 150 * time.Second,
	}, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()

	started := time.Now()
	answer, err := client.ReadShopDocument(ctx, userID, os.Getenv("SHOP_READ_EMAIL"), "user", ai.ShopDocumentRead{
		FileB64:   base64.StdEncoding.EncodeToString(data),
		MIME:      mimeFor(*file),
		MessageID: id,
		DocHint:   *hint,
		Caption:   *caption,
	})
	if err != nil {
		log.Fatalf("read %s (%d bytes) failed after %s: %v", filepath.Base(*file), len(data), elapsed(started), err)
	}

	fmt.Printf("latency:    %s\n", elapsed(started))
	fmt.Printf("doc_id:     %s\n", answer.DocID)
	fmt.Printf("doc_type:   %s\n", answer.DocType)
	fmt.Printf("flags:      %v\n", answer.Flags)
	fmt.Printf("confidence: %.2f\n", answer.Confidence)
	fmt.Printf("model_path: %v\n", answer.ModelPath)
	fmt.Printf("\n--- reply ---\n%s\n", strings.TrimSpace(answer.ReplyText))
}

func elapsed(start time.Time) string {
	return time.Since(start).Round(time.Millisecond).String()
}

func mimeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".heic":
		return "image/heic"
	default:
		return "image/jpeg"
	}
}
