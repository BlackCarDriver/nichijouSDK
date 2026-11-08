package nichijouSDK

import (
	"encoding/json"
	"os"
)

// IntegrationConfig is the credential block copied from the ChatRoom admin
// panel. It is deliberately separate from the host application's own runtime
// settings so the same file can be consumed by different games.
type IntegrationConfig struct {
	Chatroom       string `json:"chatroom"`
	RoomID         int    `json:"roomId"`
	IntegrationID  string `json:"integrationId"`
	BridgeSecret   string `json:"bridgeSecret"`
	ExchangeSecret string `json:"exchangeSecret"`
	SecretVersion  int64  `json:"secretVersion"`
}

// LoadIntegrationConfig reads an integration JSON file. A missing or empty
// file is treated as an unconfigured page-only deployment.
func LoadIntegrationConfig(path string) (IntegrationConfig, error) {
	var cfg IntegrationConfig
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// MergeIntegration lets a newer integration file override values from a
// legacy config file while retaining fields that were not copied yet.
func MergeIntegration(primary, fallback IntegrationConfig) IntegrationConfig {
	out := fallback
	if primary.Chatroom != "" {
		out.Chatroom = primary.Chatroom
	}
	if primary.RoomID != 0 {
		out.RoomID = primary.RoomID
	}
	if primary.IntegrationID != "" {
		out.IntegrationID = primary.IntegrationID
	}
	if primary.BridgeSecret != "" {
		out.BridgeSecret = primary.BridgeSecret
	}
	if primary.ExchangeSecret != "" {
		out.ExchangeSecret = primary.ExchangeSecret
	}
	if primary.SecretVersion != 0 {
		out.SecretVersion = primary.SecretVersion
	}
	return out
}
