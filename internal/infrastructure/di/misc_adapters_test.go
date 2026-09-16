package di

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestHandlePlatformMessageFailsClosedWithoutPython(t *testing.T) {
	// Texted chat must be answered only by the Python MIRIAM agent. When
	// delegation is not wired, the messenger must never fall back to a Go
	// brain — it answers with a plain apology instead.
	a := &orchestratorAdapter{}
	reply, err := a.HandlePlatformMessage(t.Context(), "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "hello", "thread-1", entities.PlatformIMessage)
	if err != nil {
		t.Fatalf("HandlePlatformMessage should not error when un-wired, got %v", err)
	}
	if reply == nil || reply.Text == "" {
		t.Fatalf("expected an apology reply, got %+v", reply)
	}
	if a.pythonDelegated() {
		t.Fatalf("pythonDelegated should be false for an un-wired adapter")
	}
}
