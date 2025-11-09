// Package docs provides Swagger API documentation
// This file contains comprehensive Swagger annotations for the STACK API
package docs

// @title STACK Service API
// @version 1.0
// @description GenZ Web3 Multi-Chain Investment Platform API - Bridging traditional finance and Web3 through a hybrid model that enables instant wealth-building
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url https://www.stackservice.com/support
// @contact.email support@stackservice.com

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token. Example: "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."

// @tag.name auth
// @tag.description Authentication and authorization endpoints

// @tag.name onboarding
// @tag.description User onboarding and KYC management

// @tag.name wallets
// @tag.description Multi-chain wallet management

// @tag.name funding
// @tag.description Deposit and funding operations

// @tag.name investing
// @tag.description Investment baskets and portfolio management

// @tag.name ai-cfo
// @tag.description AI-powered financial insights and analysis

// @tag.name due
// @tag.description Due Network integration for virtual accounts and off-ramping

// @tag.name alpaca
// @tag.description Alpaca brokerage integration for stock/ETF trading

// @tag.name admin
// @tag.description Administrative operations

// @tag.name health
// @tag.description Health check and monitoring endpoints
