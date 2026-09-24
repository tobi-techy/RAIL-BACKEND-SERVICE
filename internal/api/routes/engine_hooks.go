package routes

import "github.com/gin-gonic/gin"

// EngineHook runs after the gin engine exists (e.g. from SetupSecurityRoutesEnhanced).
// Used to mount hackathon confirmation OTP routes without editing the large SetupRoutes file.
type EngineHook func(*gin.Engine)

var engineHooks []EngineHook

func OnEngine(h EngineHook) { engineHooks = append(engineHooks, h) }

func ApplyEngineHooks(router *gin.Engine) {
	if router == nil {
		return
	}
	for _, h := range engineHooks {
		if h != nil {
			h(router)
		}
	}
}
