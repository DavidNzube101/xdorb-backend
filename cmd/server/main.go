package main

import (
	"log"
	"os"
    "time"
	"xdorb-backend/internal/api"
	"xdorb-backend/internal/bot"
	"xdorb-backend/internal/config"
    "xdorb-backend/internal"
    "xdorb-backend/internal/prpc"
    "xdorb-backend/internal/updates"
	"xdorb-backend/internal/geolocation"
    "xdorb-backend/internal/websocket"
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

	// Initialize geolocation (try local DB first, fallback to online API)
	dbPath := "./location-db/IP2LOCATION-LITE-DB11.IPV6.BIN"
	log.Printf("Initializing geolocation service (trying local DB: %s)", dbPath)
	if err := geolocation.InitDB(dbPath); err != nil {
		log.Printf("Warning: Geolocation service failed to initialize, locations will be 'Unknown': %v", err)
	} else {
		log.Println("Geolocation service initialized successfully")

		// Test with a known IP
		testIP := "8.8.8.8" // Google DNS
		log.Printf("Testing geolocation service with IP: %s", testIP)
		if loc, err := geolocation.GetLocation(testIP); err != nil {
			log.Printf("Warning: Geolocation test failed for %s: %v", testIP, err)
		} else if loc != nil {
			log.Printf("Geolocation test successful: %s -> %s", testIP, loc.GetLocationString())
		} else {
			log.Printf("Warning: Geolocation test returned nil for %s", testIP)
		}
	}

	// Set Gin mode
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize router
	r := gin.Default()

	// Add CORS middleware
	r.Use(middleware.CORS())

    // Initialize WebSocket Hub
    hub := websocket.NewHub()
    go hub.Run()

    // Initialize services for background jobs (Email Updates)
    fbService, err := internal.NewFirebaseService(cfg)
    if err != nil {
        log.Printf("Warning: Failed to init Firebase for update service: %v", err)
    }
    prpcClient := prpc.NewClient(cfg)
    
    // Start Update Service
    updateService := updates.NewService(cfg, fbService, prpcClient)
    updateService.Start()

	// Initialize API handlers
	apiHandler := api.NewHandler(cfg, hub, updateService)

	// Setup routes
	api.SetupRoutes(r, apiHandler)

    // Start background broadcast ticker
    go func() {
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        for range ticker.C {
            apiHandler.BroadcastPNodesUpdate()
            apiHandler.BroadcastStatsUpdate()
        }
    }()

	// Initialize and start Telegram bot if token is configured
	if cfg.TelegramBotToken != "" {
		tgBot, err := bot.NewBot(cfg)
		if err != nil {
			log.Printf("Failed to initialize Telegram bot: %v", err)
		} else {
			log.Println("Starting Telegram bot...")
			go func() {
				if err := tgBot.Start(); err != nil {
					log.Printf("Telegram bot stopped with error: %v", err)
				}
			}()
		}
	} else {
		log.Println("Telegram bot token not configured, bot disabled")
	}

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
