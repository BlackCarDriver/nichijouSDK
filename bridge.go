package nichijouSDK

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	secretHeader = "X-Plugin-Secret"
	bridgePath   = "/chat_room_server/plugin-bridge"

	msgHello              = "hello"
	msgStateReport        = "state_report"
	msgEventLog           = "event_log"
	msgRoomLP             = "room_lp"
	msgAck                = "ack"
	msgHelloAck           = "hello_ack"
	msgRoomLeave          = "room_leave"
	msgChatInGame         = "chat_in_game"
	msgConfigUpdate       = "config_update"
	msgForceResync        = "force_resync"
	msgRoomClosed         = "room_closed"
	msgJWTChallenge       = "jwt_challenge"
	msgJWTChallengeResult = "jwt_challenge_result"

	closeRoomClosed         = 4001
	closeSuperseded         = 4002
	closeStaleSecret        = 4003
	closeRateLimited        = 4004
	closeUnauthorized       = 4005
	closeProtocolErr        = 4006
	closeVersionUnsupported = 4009
)

// State is the state reported for one user in the plugin game.
type State struct {
	State string
	Text  string
}

// Config contains only integration transport and identity settings. It does
// not contain game-specific configuration.
type Config struct {
	ChatroomWSBase string
	RoomID         int
	IntegrationID  string
	BridgeSecret   string
	SecretVersion  int64
	InstanceID     string
	PublicKey      *rsa.PublicKey
}

// Hooks connect protocol events to the host application's game state.
type Hooks struct {
	States       func() map[string]State
	RoomLP       func() string // 房间卡片副文案（如“对局中-围棋”），nil 表示不使用该能力
	OnUserLeave  func(nick string)
	OnChat       func(nick, text string, timestamp int64)
	OnConfig     func(config map[string]interface{})
	OnRoomClosed func()
	Logger       *log.Logger
}

// Bridge maintains the server-to-server plugin room channel.
type Bridge struct {
	cfg   Config
	hooks Hooks

	lock        sync.Mutex
	conn        *websocket.Conn
	roomConfig  map[string]interface{}
	lastStateAt time.Time
	reportTimer *time.Timer
	closed      bool

	// SendHook is test-only injection. Production code should leave it nil.
	SendHook func(map[string]interface{}) bool
}

// NewBridge creates a reusable bridge client.
func NewBridge(cfg Config, hooks Hooks) *Bridge {
	return &Bridge{cfg: cfg, hooks: hooks}
}

func (b *Bridge) logf(format string, args ...interface{}) {
	if b.hooks.Logger != nil {
		b.hooks.Logger.Printf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Run establishes the channel and reconnects with bounded exponential backoff.
func (b *Bridge) Run() {
	backoff := time.Second
	for {
		err, retry := b.runOnce()
		if !retry {
			b.logf("[nichijouSDK] stop reconnecting: %v", err)
			return
		}
		b.logf("[nichijouSDK] connection ended, reconnect in %s: %v", backoff, err)
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (b *Bridge) runOnce() (error, bool) {
	url := fmt.Sprintf("%s%s?roomId=%d", b.cfg.ChatroomWSBase, bridgePath, b.cfg.RoomID)
	header := http.Header{}
	header.Set(secretHeader, b.cfg.BridgeSecret)
	conn, resp, err := (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).Dial(url, header)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone) {
			return fmt.Errorf("bridge handshake rejected status=%d", resp.StatusCode), false
		}
		return err, true
	}
	defer conn.Close()

	b.lock.Lock()
	b.conn = conn
	b.lock.Unlock()
	if err = conn.WriteJSON(map[string]interface{}{
		"type": msgHello, "protocolVersion": ProtocolVersion,
		"sdkName": SDKName, "sdkVersion": Version,
		"instanceId": b.cfg.InstanceID, "secretVersion": b.cfg.SecretVersion,
	}); err != nil {
		return err, true
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		return err, shouldRetryBridgeError(err)
	}
	var ack struct {
		Type            string                 `json:"type"`
		ProtocolVersion string                 `json:"protocolVersion"`
		Config          map[string]interface{} `json:"config"`
	}
	if err = json.Unmarshal(data, &ack); err != nil || ack.Type != msgHelloAck {
		return fmt.Errorf("hello_ack not received"), true
	}
	// Older servers may omit this field. An explicit mismatch is terminal.
	if ack.ProtocolVersion != "" && ack.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version: %s", ack.ProtocolVersion), false
	}
	b.lock.Lock()
	b.roomConfig = ack.Config
	b.lock.Unlock()
	b.logf("[nichijouSDK] connected, protocol=%s sdk=%s version=%s, room config: %+v", ProtocolVersion, SDKName, Version, ack.Config)
	b.reportFullState()
	b.SyncRoomLP()
	_ = conn.SetReadDeadline(time.Time{})

	for {
		_, data, err = conn.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				b.logf("[nichijouSDK] connection closed code=%d reason=%s", ce.Code, ce.Text)
			}
			return err, shouldRetryBridgeError(err)
		}
		b.handleMessage(data)
	}
}

func shouldRetryBridgeError(err error) bool {
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case closeRoomClosed, closeSuperseded, closeStaleSecret, closeUnauthorized, closeProtocolErr, closeVersionUnsupported:
			return false
		}
	}
	return true
}

func (b *Bridge) handleMessage(data []byte) {
	var head struct {
		Type      string `json:"type"`
		MsgID     string `json:"msgId"`
		Nick      string `json:"nick"`
		Text      string `json:"text"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return
	}
	switch head.Type {
	case msgRoomLeave:
		if b.hooks.OnUserLeave != nil {
			b.hooks.OnUserLeave(head.Nick)
		}
		b.ack(head.MsgID, true, "")
	case msgConfigUpdate:
		var upd struct {
			Config map[string]interface{} `json:"config"`
		}
		_ = json.Unmarshal(data, &upd)
		b.lock.Lock()
		b.roomConfig = upd.Config
		b.lock.Unlock()
		if b.hooks.OnConfig != nil {
			b.hooks.OnConfig(upd.Config)
		}
		b.ack(head.MsgID, true, "")
	case msgForceResync:
		b.reportFullState()
	case msgChatInGame:
		if b.hooks.OnChat != nil {
			b.hooks.OnChat(head.Nick, head.Text, head.Timestamp)
		}
	case msgJWTChallenge:
		b.handleJWTChallenge(data)
	case msgRoomClosed:
		b.ack(head.MsgID, true, "")
		b.lock.Lock()
		b.closed = true
		b.lock.Unlock()
		if b.hooks.OnRoomClosed != nil {
			b.hooks.OnRoomClosed()
		}
	}
}

func (b *Bridge) ack(msgID string, ok bool, errMsg string) {
	if msgID != "" {
		b.send(map[string]interface{}{"type": msgAck, "msgId": msgID, "ok": ok, "error": errMsg})
	}
}

func (b *Bridge) send(payload map[string]interface{}) bool {
	if b.SendHook != nil {
		return b.SendHook(payload)
	}
	b.lock.Lock()
	defer b.lock.Unlock()
	if b.conn == nil {
		return false
	}
	_ = b.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := b.conn.WriteJSON(payload); err != nil {
		b.logf("[nichijouSDK] send failed: %v", err)
		return false
	}
	return true
}

// ScheduleFullReport coalesces rapid state changes and guarantees a trailing
// full snapshot, so simultaneous changes cannot lose one user's state.
func (b *Bridge) ScheduleFullReport() {
	b.lock.Lock()
	since := time.Since(b.lastStateAt)
	if since >= time.Second {
		b.lastStateAt = time.Now()
		b.lock.Unlock()
		b.reportFullState()
		return
	}
	if b.reportTimer != nil {
		b.lock.Unlock()
		return
	}
	b.reportTimer = time.AfterFunc(time.Second-since, func() {
		b.lock.Lock()
		b.reportTimer = nil
		b.lastStateAt = time.Now()
		b.lock.Unlock()
		b.reportFullState()
	})
	b.lock.Unlock()
}

func (b *Bridge) reportFullState() {
	states := map[string]State{}
	if b.hooks.States != nil {
		states = b.hooks.States()
	}
	items := make([]map[string]interface{}, 0, len(states))
	for nick, state := range states {
		items = append(items, map[string]interface{}{"nick": nick, "state": state.State, "text": state.Text, "since": time.Now().Unix()})
	}
	b.send(map[string]interface{}{"type": msgStateReport, "full": true, "states": items})
}

// EventLog writes a key milestone to the room's public event stream.
func (b *Bridge) EventLog(text string) {
	b.send(map[string]interface{}{"type": msgEventLog, "text": text, "timestamp": time.Now().Unix()})
}

// SetRoomLP updates the room card subtitle (hallRoomLP) shown in the hall.
// The text is plain and length-limited on the server side.
func (b *Bridge) SetRoomLP(text string) {
	b.send(map[string]interface{}{"type": msgRoomLP, "text": text})
}

// SyncRoomLP sends the current subtitle once; used after (re)connect so the
// card catches up even if the chat server restarted meanwhile.
func (b *Bridge) SyncRoomLP() {
	if b.hooks.RoomLP != nil {
		b.SetRoomLP(b.hooks.RoomLP())
	}
}

func (b *Bridge) handleJWTChallenge(data []byte) {
	var challenge struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &challenge); err != nil || challenge.Token == "" {
		return
	}
	result := map[string]interface{}{"type": msgJWTChallengeResult, "ok": false}
	claims, err := VerifyJWT(challenge.Token, b.cfg.PublicKey, b.cfg.IntegrationID, b.cfg.RoomID)
	if err != nil {
		result["error"] = err.Error()
		b.send(result)
		b.logf("[nichijouSDK] identity challenge failed: %v", err)
		return
	}
	result["ok"] = true
	result["sub"] = claims.Sub
	result["aud"] = claims.Aud
	result["room"] = claims.Room
	b.send(result)
	b.logf("[nichijouSDK] identity challenge passed: sub=%s aud=%s room=%d", claims.Sub, claims.Aud, claims.Room)
}
