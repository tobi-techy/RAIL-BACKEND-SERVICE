package confirmation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	svc "github.com/rail-service/rail_service/internal/domain/services/confirmation"
)

func testHandler() (*Handler, *svc.Service) {
	s := svc.NewService(svc.Config{
		TokenSecret: "handler-test-secret-1234567890",
		ConfirmBase: "https://example.com/confirm",
	}, nil, nil)
	s.RegisterExecutor("transfer.send", svc.TransferSendExecutor(&stubP2P{}))
	return NewHandler(s, nil, nil), s
}

type stubP2P struct{}

func (s *stubP2P) Send(ctx context.Context, senderID uuid.UUID, req *entities.P2PSendRequest) (*entities.P2PTransferResponse, error) {
	return &entities.P2PTransferResponse{Transfer: &entities.P2PTransfer{ID: uuid.New()}}, nil
}

func TestCreateValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := testHandler()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", uuid.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/confirmations",
		strings.NewReader(`{"action":"nope"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Create(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: got %d want 400", w.Code)
	}

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Set("user_id", uuid.New())
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/confirmations",
		strings.NewReader(`{"action":"transfer.send","payload":{"to":"@tobi","amount":"20000"}}`))
	c2.Request.Header.Set("Content-Type", "application/json")
	h.Create(c2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("create: got %d body %s", w2.Code, w2.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["action_id"] == nil || body["confirm_url"] == nil {
		t.Fatalf("missing fields: %v", body)
	}
	delivered, ok := body["card_delivered"].(bool)
	if ok && delivered {
		t.Fatal("nil sender must not claim delivery")
	}
}

func markCall(h *Handler, id, payload string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/confirmations/"+id+"/mark",
		strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Mark(c)
	return w
}

func TestMarkTerminalSync(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, s := testHandler()

	ctx := context.Background()
	uid := uuid.New()
	rec, _, err := s.Create(ctx, svc.CreateInput{
		UserID: uid, Action: "transfer.send",
		Payload: map[string]any{
			"to": "Funsho", "amount": "20000",
			"miriam_confirm_id": "confirm_chat1", "thread_id": "space1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Chat settles first: the live card must show completed.
	w := markCall(h, rec.ID.String(), `{"state":"completed","result":"Sent 20000 (ref ab12cd34)"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("mark completed: got %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	conf, _ := body["confirmation"].(map[string]any)
	if conf["state"] != "completed" {
		t.Fatalf("state = %v", conf["state"])
	}

	// Terminal replay is a 200 no-op with the current state.
	w2 := markCall(h, rec.ID.String(), `{"state":"rejected","result":"late"}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("mark replay: got %d", w2.Code)
	}
	var body2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &body2); err != nil {
		t.Fatal(err)
	}
	conf2, _ := body2["confirmation"].(map[string]any)
	if conf2["state"] != "completed" {
		t.Fatalf("replay must keep completed, got %v", conf2["state"])
	}

	// Non-terminal states are refused.
	w3 := markCall(h, rec.ID.String(), `{"state":"approved"}`)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("mark approved: got %d want 400", w3.Code)
	}
	// Unknown ids are 404.
	w4 := markCall(h, uuid.NewString(), `{"state":"completed"}`)
	if w4.Code != http.StatusNotFound {
		t.Fatalf("mark unknown: got %d want 404", w4.Code)
	}
	// Malformed ids are 400.
	w5 := markCall(h, "not-a-uuid", `{"state":"completed"}`)
	if w5.Code != http.StatusBadRequest {
		t.Fatalf("mark bad id: got %d want 400", w5.Code)
	}
}
