package main

import (
	"fmt"
	"os"

	"github.com/rail-service/rail_service/internal/app"
)

// @title Rail Service API
// @version 1.0
// @description Rules-based capital engine — money splits itself the moment it arrives.
// @termsOfService https://www.getrail.app/terms

// @contact.name API Support
// @contact.url https://www.getrail.app/support
// @contact.email support@getrail.app

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

func main() {
	application := app.NewApplication()

	if err := application.Initialize(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize application: %v\n", err)
		os.Exit(1)
	}

	if err := application.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start application: %v\n", err)
		os.Exit(1)
	}

	application.WaitForShutdown()

	if err := application.Shutdown(); err != nil {
		fmt.Fprintf(os.Stderr, "Error during shutdown: %v\n", err)
		os.Exit(1)
	}
}
