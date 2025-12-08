package main

import (
	"log"
	"os"
	"xdorb-backend/internal/api"
	"xdorb-backend/internal/config"
	"xdorb-backend/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// Initialize configuration
	cfg := config.Load()

	// Set Gin mode
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize router
	r := gin.Default()

	// Add CORS middleware
	r.Use(middleware.CORS())

	// Initialize API handlers
	apiHandler := api.NewHandler(cfg)

	// Setup routes
	api.SetupRoutes(r, apiHandler)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
