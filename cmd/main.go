package main

import (
	"fmt"
	"os"

	"github.com/rail-service/rail_service/internal/app"
)

// @title Stack Service API
// @version 1.0
// @description GenZ Web3 Multi-Chain Investment Platform API
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.stackservice.com/support
// @contact.email support@stackservice.com

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

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
