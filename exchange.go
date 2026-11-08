package nichijouSDK

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// ExchangeConfig configures the server-side ticket exchange proxy used by
// template B. The browser must call the host application, never ChatRoom
// directly, so ExchangeSecret remains server-side.
type ExchangeConfig struct {
	ChatroomBase   string
	ExchangeSecret string
	Client         *http.Client
}

// ExchangeHandler is an http.Handler for POST /exchange.
type ExchangeHandler struct{ cfg ExchangeConfig }

func NewExchangeHandler(cfg ExchangeConfig) *ExchangeHandler {
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ExchangeHandler{cfg: cfg}
}

func (h *ExchangeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil || strings.TrimSpace(req.Ticket) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body, _ := json.Marshal(map[string]string{"ticket": req.Ticket})
	url := strings.TrimRight(h.cfg.ChatroomBase, "/") + "/chat_room_server/plugin-ticket-exchange"
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, "exchange failed", http.StatusInternalServerError)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Plugin-Exchange-Secret", h.cfg.ExchangeSecret)
	resp, err := h.cfg.Client.Do(httpReq)
	if err != nil {
		http.Error(w, "chatroom unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	var result struct {
		Status int    `json:"status"`
		Msg    string `json:"msg"`
		Token  string `json:"token"`
		Nick   string `json:"nick"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		http.Error(w, "unexpected response", http.StatusBadGateway)
		return
	}
	if result.Status != 0 {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": result.Msg})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"token": result.Token, "nick": result.Nick})
}
