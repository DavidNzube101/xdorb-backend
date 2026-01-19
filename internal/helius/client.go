package helius

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"xdorb-backend/internal/config"
)

type Client struct {
	apiKey string
	client *http.Client
}

type DASAsset struct {
	ID        string `json:"id"`
	Interface string `json:"interface"`
	Content   struct {
		Metadata struct {
			Name        string `json:"name"`
			Symbol      string `json:"symbol"`
			Description string `json:"description"`
		} `json:"metadata"`
		Links struct {
			Image string `json:"image"`
		} `json:"links"`
		Files []struct {
			URI string `json:"uri"`
		} `json:"files"`
	} `json:"content"`
}

type DASResponse struct {
	Result struct {
		Items []DASAsset `json:"items"`
	} `json:"result"`
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		apiKey: cfg.HeliusAPIKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) GetAssetsByOwner(ownerAddress string) ([]DASAsset, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("Helius API key not configured")
	}

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", c.apiKey)

	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "xdorb-assets",
		"method":  "getAssetsByOwner",
		"params": map[string]interface{}{
			"ownerAddress": ownerAddress,
			"page":         1,
			"limit":        100, // Fetch up to 100 assets
			"displayOptions": map[string]bool{
				"showUnverifiedCollections": true,
				"showCollectionMetadata":    true,
			},
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Helius API returned status: %d", resp.StatusCode)
	}

	var dasResp DASResponse
	if err := json.NewDecoder(resp.Body).Decode(&dasResp); err != nil {
		return nil, err
	}

	return dasResp.Result.Items, nil
}
