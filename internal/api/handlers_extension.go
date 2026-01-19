package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"xdorb-backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// DeveloperAuth middleware for custom API keys
func (h *Handler) DeveloperAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("x-api-key")
		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, models.APIResponse{Error: "API key required"})
			c.Abort()
			return
		}

		// Hash the key to check against database
		hash := sha256.Sum256([]byte(apiKey))
		hashedKey := hex.EncodeToString(hash[:])

		// Check Firestore
		keyData, err := h.firebase.GetAPIKeyByHash(c.Request.Context(), hashedKey)
		if err != nil || keyData == nil {
			c.JSON(http.StatusUnauthorized, models.APIResponse{Error: "Invalid API key"})
			c.Abort()
			return
		}

        // Update LastUsedAt (async to not block)
        go func() {
            keyData.LastUsedAt = time.Now()
            h.firebase.SaveAPIKey(context.Background(), keyData)
        }()

		c.Next()
	}
}

// SubscribeToNode handles email subscriptions
func (h *Handler) SubscribeToNode(c *gin.Context) {
    id := c.Param("id")
    var req struct {
        Email     string `json:"email"`
        Frequency string `json:"frequency"` // "daily" or "twice_daily"
    }

    if err := c.BindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, models.APIResponse{Error: "Invalid request body"})
        return
    }

    if req.Frequency != "daily" && req.Frequency != "twice_daily" {
        c.JSON(http.StatusBadRequest, models.APIResponse{Error: "Invalid frequency. Must be 'daily' or 'twice_daily'"})
        return
    }

    sub := &models.EmailSubscriber{
        Email:     req.Email,
        PNodeID:   id,
        Frequency: req.Frequency,
        CreatedAt: time.Now(),
    }

    if err := h.firebase.SaveEmailSubscriber(c.Request.Context(), sub); err != nil {
        logrus.Error("Failed to save email subscriber:", err)
        c.JSON(http.StatusInternalServerError, models.APIResponse{Error: "Failed to save subscription"})
        return
    }

    c.JSON(http.StatusOK, models.APIResponse{Message: "Subscribed successfully"})
}

// TestEmail sends a test email update immediately
func (h *Handler) TestEmail(c *gin.Context) {
    id := c.Param("id")
    var req struct {
        Email string `json:"email"`
    }

    if err := c.BindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, models.APIResponse{Error: "Invalid request body"})
        return
    }

    if h.updateService == nil {
        c.JSON(http.StatusServiceUnavailable, models.APIResponse{Error: "Update service not initialized"})
        return
    }

    if err := h.updateService.SendTestEmail(id, req.Email); err != nil {
        logrus.Error("Failed to send test email:", err)
        c.JSON(http.StatusInternalServerError, models.APIResponse{Error: err.Error()})
        return
    }

    c.JSON(http.StatusOK, models.APIResponse{Message: "Test email sent successfully"})
}

// GenerateAPIKey creates a new API key for a developer
func (h *Handler) GenerateAPIKey(c *gin.Context) {
    var req struct {
        WalletAddress string `json:"walletAddress"`
        // Signature string `json:"signature"` // TODO: Implement signature verification
    }

    if err := c.BindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, models.APIResponse{Error: "Invalid request body"})
        return
    }

    // Generate a secure random API key
    bytes := make([]byte, 32)
    if _, err := rand.Read(bytes); err != nil {
        c.JSON(http.StatusInternalServerError, models.APIResponse{Error: "Failed to generate key"})
        return
    }
    apiKey := "sk_live_" + hex.EncodeToString(bytes)

    // Hash it for storage
    hash := sha256.Sum256([]byte(apiKey))
    hashedKey := hex.EncodeToString(hash[:])

    keyModel := &models.APIKey{
        WalletAddress: req.WalletAddress,
        HashedKey:     hashedKey,
        CreatedAt:     time.Now(),
        LastUsedAt:    time.Now(),
        IsActive:      true,
    }

    if err := h.firebase.SaveAPIKey(c.Request.Context(), keyModel); err != nil {
        logrus.Error("Failed to save API key:", err)
        c.JSON(http.StatusInternalServerError, models.APIResponse{Error: "Failed to create API key"})
        return
    }

    // Return the raw key ONLY ONCE
    c.JSON(http.StatusOK, models.APIResponse{Data: map[string]string{
        "apiKey": apiKey,
        "message": "Store this key safely. It will not be shown again.",
    }})
}

// V1 Handlers

func (h *Handler) GetV1PNodes(c *gin.Context) {
    // Reuse existing logic
    h.GetPNodes(c)
}

func (h *Handler) GetV1PNodeByID(c *gin.Context) {
    // Reuse existing logic
    h.GetPNodeByID(c)
}

func (h *Handler) GetV1Analytics(c *gin.Context) {
    // Reuse existing logic
    h.GetAnalytics(c)
}

func (h *Handler) GetV1Network(c *gin.Context) {
    // Show distribution of nodes across the world
    h.GetGeo(c)
}

func (h *Handler) GetV1Region(c *gin.Context) {
    region := c.Param("region")
    // Reuse GetPNodes logic with region filter
    c.Request.URL.RawQuery = "region=" + region
    h.GetPNodes(c)
}

func (h *Handler) GetV1Leaderboard(c *gin.Context) {
    h.GetLeaderboard(c)
}

func (h *Handler) GetV1LeaderboardSeason(c *gin.Context) {
    
    h.GetLeaderboard(c)
}

// GetPNodeNFTs fetches NFTs associated with the pNode's manager wallet
func (h *Handler) GetPNodeNFTs(c *gin.Context) {
    id := c.Param("id")
    if id == "" {
        c.JSON(http.StatusBadRequest, models.APIResponse{Error: "pNode ID is required"})
        return
    }

    
    
    registeredMap, err := h.getRegisteredPNodesSet()
    walletAddress := id // Default to pNode ID (assuming it might be the wallet)
    
    if err == nil {
        if manager, ok := registeredMap[id]; ok && manager != "" {
            walletAddress = manager
        }
    }

    
    assets, err := h.heliusClient.GetAssetsByOwner(walletAddress)
    if err != nil {
        logrus.Errorf("Failed to fetch assets for %s: %v", walletAddress, err)
        c.JSON(http.StatusInternalServerError, models.APIResponse{Error: "Failed to fetch wallet assets"})
        return
    }

    var nfts []map[string]string
    for _, asset := range assets {
        
        if asset.Interface == "V1_NFT" || asset.Interface == "ProgrammableNFT" || asset.Interface == "MplsCore" {
             
             name := strings.ToLower(asset.Content.Metadata.Name)
             desc := strings.ToLower(asset.Content.Metadata.Description)
             
             isRelevant := strings.Contains(name, "xandeum") || 
                           strings.Contains(desc, "xandeum") ||
                           strings.Contains(name, "pioneer") ||
                           strings.Contains(name, "genesis") ||
                           strings.Contains(name, "titan") ||
                           strings.Contains(name, "xeno")

             if isRelevant {
                 image := asset.Content.Links.Image
                 if image == "" && len(asset.Content.Files) > 0 {
                     image = asset.Content.Files[0].URI
                 }

                 nfts = append(nfts, map[string]string{
                     "name": asset.Content.Metadata.Name,
                     "image": image,
                     "description": asset.Content.Metadata.Description,
                 })
             }
        }
    }

    c.JSON(http.StatusOK, models.APIResponse{Data: nfts})
}
