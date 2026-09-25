package investment

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	investmentsvc "github.com/rail-service/rail_service/internal/domain/services/investment"
)

// Funding failures return (FAILED body, non-nil error), which routes through
// respond(), not respondAction(). The 502 mapping must fire there or agent
// retry/alerting contracts keyed on 502 never trigger.
func TestFailedActionStatus(t *testing.T) {
	failed := &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionFailed}
	if !failedActionStatus(failed) {
		t.Fatal("FAILED enroll response must map to 502")
	}
	completed := &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionCompleted}
	if failedActionStatus(completed) {
		t.Fatal("COMPLETED response must not map to 502")
	}
	if failedActionStatus(nil) {
		t.Fatal("nil payload must not map to 502")
	}
	if failedActionStatus("not a response") {
		t.Fatal("unknown payload type must not map to 502")
	}
	contrib := &investmentsvc.UserContributeResponse{Status: entities.InvestmentActionFailed}
	if !failedActionStatus(contrib) {
		t.Fatal("FAILED contribute response must map to 502")
	}
	order := &entities.InvestmentOrderResponse{Status: entities.InvestmentActionRejected}
	if failedActionStatus(order) {
		t.Fatal("REJECTED response must not map to 502")
	}
}
