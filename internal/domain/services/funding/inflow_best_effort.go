package funding

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// inflowNotifyBudget bounds one credit projection to the Python brain. The
// report is best-effort: it runs after the credit has committed, its outcome
// never changes what the provider webhook answers, and a slow or missing
// Python can hold a webhook for at most this long.
//
// Why best-effort: the old contract made a failed report return an error so
// the provider would redeliver (Bridge and Graph got a 503). MIRIAM PR #51
// removes POST /api/v1/money/inflow, so every report would 404 and every NGN
// deposit webhook would be redelivered forever. Go is the money authority;
// Python no longer keeps a spendable ledger and Mimi never moves money, so a
// missed projection is a log line and a metric, never a webhook failure.
var inflowNotifyBudget = 5 * time.Second

// reportInflowBestEffort sends one credit projection within inflowNotifyBudget
// and returns the notifier's error for logging only. Callers must not return
// it to the webhook path.
func reportInflowBestEffort(ctx context.Context, notifier InflowNotifier, userID uuid.UUID, paymentID, amount, currency, sourceRaw string, identity entities.InflowIdentity) error {
	if notifier == nil {
		return nil
	}
	nctx, cancel := context.WithTimeout(ctx, inflowNotifyBudget)
	defer cancel()
	return notifier.NotifyInflow(nctx, userID, paymentID, amount, currency, sourceRaw, identity)
}
