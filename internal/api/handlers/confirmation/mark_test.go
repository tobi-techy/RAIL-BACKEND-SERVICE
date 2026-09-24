package confirmation

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func markTestContext(t *testing.T, h *Handler, id uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id.String()}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/confirmations/"+id.String()+"/mark",
		strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Mark(c)
	return w
}

func TestMarkCompletedFromPending(t *testing.T) {
	h, _ := testHandler()
	uid := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", uid)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/confirmations",
		strings.NewReader(`{"action":"transfer.send","payload":{"to":"@tobi","amount":"20000"}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Create(c)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d body %s", w.Code, w.Body.String())
	}
	actionID := extractActionID(t, w.Body.String())

	w2 := markTestContext(t, h, actionID, `{"state":"completed","result":"Sent 20000"}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("mark: got %d body %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), `"completed"`) {
		t.Fatalf("mark did not complete the card: %s", w2.Body.String())
	}

	// Terminal replay is a 200 no-op with current state.
	w3 := markTestContext(t, h, actionID, `{"state":"completed","result":"other"}`)
	if w3.Code != http.StatusOK || !strings.Contains(w3.Body.String(), `"completed"`) {
		t.Fatalf("replay: got %d body %s", w3.Code, w3.Body.String())
	}
}

func TestMarkValidation(t *testing.T) {
	h, _ := testHandler()
	id := uuid.New()
	// Non-terminal state rejected.
	if w := markTestContext(t, h, id, `{"state":"pending"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("non-terminal mark: got %d", w.Code)
	}
	// Unknown action id.
	if w := markTestContext(t, h, id, `{"state":"completed"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d", w.Code)
	}
	// Malformed id.
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"state":"completed"}`))
	h.Mark(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: got %d", w.Code)
	}
}

func extractActionID(t *testing.T, body string) uuid.UUID {
	t.Helper()
	start := strings.Index(body, `"action_id":"`)
	if start < 0 {
		t.Fatalf("no action_id in %s", body)
	}
	rest := body[start+len(`"action_id":"`):]
	end := strings.Index(rest, `"`)
	id, err := uuid.Parse(rest[:end])
	if err != nil {
		t.Fatalf("bad action_id: %v", err)
	}
	return id
}
