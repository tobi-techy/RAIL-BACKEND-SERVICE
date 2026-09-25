package confirmation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
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

// fakeTxHandler implements svc.TxAssertionService for handler tests.
type fakeTxHandler struct {
	hasCreds  bool
	beginErr  error
	finishErr error
}

func (f *fakeTxHandler) BeginTransactionAssertion(ctx context.Context, userID uuid.UUID, email string) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	if f.beginErr != nil {
		return nil, nil, f.beginErr
	}
	return &protocol.CredentialAssertion{
		Response: protocol.PublicKeyCredentialRequestOptions{
			Challenge:        protocol.URLEncodedBase64("handler-challenge"),
			RelyingPartyID:   "example.com",
			UserVerification: protocol.VerificationRequired,
		},
	}, &webauthn.SessionData{Challenge: "handler-challenge"}, nil
}

func (f *fakeTxHandler) FinishTransactionAssertion(ctx context.Context, userID uuid.UUID, email string, session *webauthn.SessionData, response *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error) {
	if f.finishErr != nil {
		return nil, f.finishErr
	}
	return &webauthn.Credential{}, nil
}

func (f *fakeTxHandler) HasCredentials(ctx context.Context, userID uuid.UUID) (bool, error) {
	return f.hasCreds, nil
}

func passkeyHandler() (*Handler, *svc.Service, uuid.UUID, string, string) {
	h, s := testHandler()
	tx := &fakeTxHandler{hasCreds: true}
	s.SetTxAssertion(tx)
	s.SetUserEmailLookup(func(ctx context.Context, userID uuid.UUID) (string, error) {
		return "user@example.com", nil
	})
	ctx := context.Background()
	uid := uuid.New()
	rec, url, err := s.Create(ctx, svc.CreateInput{
		UserID: uid, Action: "transfer.send",
		Payload: map[string]any{"to": "Funsho", "amount": "20000"},
	})
	if err != nil {
		panic(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	return h, s, uid, rec.ID.String(), tok
}

func TestAssertionOptionsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _, id, tok := passkeyHandler()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodGet, "/confirm/"+id+"/assertion-options?t="+tok, nil)
	h.AssertionOptions(c)
	if w.Code != http.StatusOK {
		t.Fatalf("options: got %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	opts, ok := body["options"].(map[string]any)
	if !ok {
		t.Fatalf("missing options: %v", body)
	}
	pub, ok := opts["publicKey"].(map[string]any)
	if !ok {
		t.Fatalf("missing publicKey: %v", opts)
	}
	if pub["rpId"] != "example.com" {
		t.Fatalf("rpId = %v", pub["rpId"])
	}
	if pub["userVerification"] != "required" {
		t.Fatalf("userVerification = %v", pub["userVerification"])
	}
}

func TestAssertionOptionsNoPasskey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, s, _, id, _ := passkeyHandler()
	s.SetTxAssertion(&fakeTxHandler{beginErr: errors.New("no passkey enrolled")})
	// Re-mint a token via fetch path: use a fresh card through the same service.
	ctx := context.Background()
	uid := uuid.New()
	rec, url, _ := s.Create(ctx, svc.CreateInput{UserID: uid, Action: "transfer.send", Payload: map[string]any{"to": "x", "amount": "1"}})
	tok := url[strings.Index(url, "?t=")+3:]

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: rec.ID.String()}}
	_ = id
	c.Request = httptest.NewRequest(http.MethodGet, "/confirm/"+rec.ID.String()+"/assertion-options?t="+tok, nil)
	h.AssertionOptions(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 passkey_setup, got %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["passkey_setup"] != true {
		t.Fatalf("missing passkey_setup flag: %v", body)
	}
}

func TestApproveWithAssertionEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _, id, tok := passkeyHandler()

	// Fetch advertises the passkey state for the extension UI choice.
	wf := httptest.NewRecorder()
	cf, _ := gin.CreateTestContext(wf)
	cf.Params = gin.Params{{Key: "id", Value: id}}
	cf.Request = httptest.NewRequest(http.MethodGet, "/confirm/"+id+"?t="+tok, nil)
	h.Fetch(cf)
	var fbody map[string]any
	if err := json.Unmarshal(wf.Body.Bytes(), &fbody); err != nil {
		t.Fatal(err)
	}
	if fbody["passkey_registered"] != true {
		t.Fatalf("fetch must advertise passkey_registered: %v", fbody)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodPost, "/confirm/"+id+"/approve",
		strings.NewReader(`{"t":"`+tok+`","biometric":"pass","assertion":{"id":"x","type":"public-key"}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Approve(c)
	// The stub assertion body won't parse as real WebAuthn: expect 422, which
	// still proves routing reached ApproveWithAssertion (not legacy Approve —
	// legacy would have completed and returned 200 with the stub executor).
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("approve with assertion: got %d body %s (want 422 parse refusal)", w.Code, w.Body.String())
	}
}
