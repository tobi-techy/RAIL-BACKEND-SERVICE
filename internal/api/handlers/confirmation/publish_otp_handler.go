package confirmation

import "github.com/rail-service/rail_service/internal/platform/runtimereg"

// publishOTPHandler exposes the confirmation handler for hackathon OTP route
// mounting via engine hooks (avoids di→routes import cycles).
func publishOTPHandler(h *Handler) {
	if h == nil {
		return
	}
	runtimereg.Set("confirmation_handlers", h)
}
