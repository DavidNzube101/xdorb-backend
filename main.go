package main

import (
	"log"

	"github.com/gin-gonic/gin"
	"xdorb-backend/internal/api"
	"xdorb-backend/internal/config"
	"xdorb-backend/internal/geolocation"
)

func main() {
	// Load config
	cfg := config.Load()

	// Initialize geolocation DB (optional)
	if err := geolocation.InitDB("./location-db/IP2LOCATION-LITE-DB11.IPV6.BIN/IP2LOCATION-LITE-DB11.IPV6.BIN"); err != nil {
		log.Println("Warning: Failed to initialize geolocation DB, locations will be unknown:", err)
	} else {
		defer geolocation.CloseDB()
	}

	handler := api.NewHandler(cfg)
	r := gin.Default()
	api.SetupRoutes(r, handler)

	log.Printf("Starting server on port %s", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
