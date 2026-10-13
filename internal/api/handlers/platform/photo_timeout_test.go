package platform

import (
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraplatform "github.com/rail-service/rail_service/internal/infrastructure/platform"
)

// unknownRepo reports every sender as unlinked, so a photo reaches the OCR step
// and then the "link your account" reply, with no database.
type unknownRepo struct{ blipRepo }

func (unknownRepo) GetByPlatformUser(context.Context, entities.Platform, string) (*entities.PlatformIdentity, error) {
	return nil, sql.ErrNoRows
}

func (unknownRepo) GetByID(context.Context, uuid.UUID) (*entities.PlatformIdentity, error) {
	return nil, sql.ErrNoRows
}

// slowOCR stands in for a slow OCR read.
type slowOCR struct{ delay time.Duration }

func (slowOCR) Available() bool { return true }
func (s slowOCR) SummarizeReceipt(context.Context, []byte, string) (string, error) {
	time.Sleep(s.delay)
	return "Got it, Shoprite.", nil
}

// inboundServer runs HandleInbound on a real http.Server with the given
// WriteTimeout, which is how the platform runs it (30s there, 250ms here).
func inboundServer(t *testing.T, photoTimeout time.Duration, ocrDelay time.Duration) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewPlatformHandlerWithLogger(nil, "", nil)
	h.SetPhotoTurnTimeout(photoTimeout)
	proc := infraplatform.NewProcessor(
		infraplatform.NewUserResolver(unknownRepo{}),
		nil, nil, nil, nil,
		func(context.Context, *infraplatform.OutboundMessage) error { return nil },
	)
	proc.SetReceiptVision(slowOCR{delay: ocrDelay})
	router := gin.New()
	router.POST("/inbound", h.HandleInbound(proc))
	srv := httptest.NewUnstartedServer(router)
	srv.Config.WriteTimeout = 250 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func postInbound(t *testing.T, srv *httptest.Server, body string) (int, error) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/inbound", "application/json", strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func photoBody() string {
	return `{"platform":"imessage","user_id":"+15551234567","thread_id":"t1","msg_id":"m1","is_image":true,"image_mime":"image/jpeg","image_b64":"` +
		base64.StdEncoding.EncodeToString([]byte("jpeg")) + `"}`
}

func TestHandleInbound_ASlowPhotoOutlivesTheServerWriteTimeout(t *testing.T) {
	// The read takes 600ms, well past the 250ms WriteTimeout.
	srv := inboundServer(t, 5*time.Second, 600*time.Millisecond)

	code, err := postInbound(t, srv, photoBody())
	if err != nil || code != http.StatusOK {
		t.Fatalf("a slow photo must still be answered: status=%d err=%v", code, err)
	}
}

func TestHandleInbound_ASlowTextTurnStillHitsTheServerWriteTimeout(t *testing.T) {
	// Control: the extension is for photos only. The same delay on a typed
	// message keeps the server's WriteTimeout, so the deadline is what differs.
	body := `{"platform":"imessage","user_id":"+15551234567","thread_id":"t1","msg_id":"m2","text":"hello"}`
	code, err := postInbound(t, inboundServerWithSlowText(t), body)
	if err == nil && code == http.StatusOK {
		t.Fatalf("a slow non-photo turn was answered past the WriteTimeout (status=%d)", code)
	}
}

func inboundServerWithSlowText(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/inbound", func(c *gin.Context) {
		h := NewPlatformHandlerWithLogger(nil, "", nil)
		body, _ := io.ReadAll(c.Request.Body)
		h.allowSlowPhotoResponse(c, body)
		time.Sleep(600 * time.Millisecond)
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	srv := httptest.NewUnstartedServer(router)
	srv.Config.WriteTimeout = 250 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleInbound_APhotoPastItsOwnBudgetIsStillCutOff(t *testing.T) {
	srv := inboundServer(t, 300*time.Millisecond, 900*time.Millisecond)

	code, err := postInbound(t, srv, photoBody())
	if err == nil && code == http.StatusOK {
		t.Fatalf("the photo budget is a bound, not an invitation (status=%d)", code)
	}
}
