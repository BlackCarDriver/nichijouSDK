// Package nichijouSDK contains the reusable server-side integration pieces for
// a ChatRoom plugin room. Game rules and browser UI should remain in the host
// application and depend on this package through its small callbacks.
package nichijouSDK

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// DefaultIssuer is the issuer used by the ChatRoom server.
	DefaultIssuer    = "nichijou-chatroom"
	DefaultClockSkew = 60 * time.Second
)

// Claims is the verified identity and display profile issued by ChatRoom.
// Only use the claims after VerifyJWT returns successfully.
type Claims struct {
	Iss        string `json:"iss"`
	Aud        string `json:"aud"`
	Sub        string `json:"sub"`
	Jti        string `json:"jti"`
	Room       int    `json:"room"`
	Registered bool   `json:"registered"`
	RoleID     int    `json:"roleId"`
	Avatar     string `json:"avatar"`
	FrameID    int    `json:"frameId"`
	Frame      string `json:"frame"`
	Power      int64  `json:"power"`
	Iat        int64  `json:"iat"`
	Exp        int64  `json:"exp"`
}

// LoadPublicKey reads the public key published by ChatRoom.
func LoadPublicKey(path string) (*rsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("public key PEM parse failed")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not RSA")
	}
	return rsaPub, nil
}

// VerifyJWT validates the signing algorithm, signature, issuer, audience,
// room, expiry, issue time and subject. The audience is the IntegrationID;
// room validation prevents a token from being reused in another room owned by
// the same integration.
func VerifyJWT(token string, pub *rsa.PublicKey, integrationID string, roomID int) (*Claims, error) {
	return VerifyJWTWithOptions(token, pub, VerifyOptions{
		Issuer:        DefaultIssuer,
		IntegrationID: integrationID,
		RoomID:        roomID,
		ClockSkew:     DefaultClockSkew,
	})
}

// VerifyOptions controls the values that identify a plugin room.
type VerifyOptions struct {
	Issuer        string
	IntegrationID string
	RoomID        int
	ClockSkew     time.Duration
}

// VerifyJWTWithOptions is the configurable form of VerifyJWT. A zero issuer
// or clock skew uses the protocol defaults.
func VerifyJWTWithOptions(token string, pub *rsa.PublicKey, opts VerifyOptions) (*Claims, error) {
	if pub == nil {
		return nil, fmt.Errorf("public key is not configured")
	}
	if opts.Issuer == "" {
		opts.Issuer = DefaultIssuer
	}
	if opts.ClockSkew <= 0 {
		opts.ClockSkew = DefaultClockSkew
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid token header")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err = json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("invalid token header")
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported signing algorithm: %s", header.Alg)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid token signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, fmt.Errorf("signature verification failed")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid token payload")
	}
	var claims Claims
	if err = json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid token payload")
	}

	now := time.Now()
	nowUnix := now.Unix()
	skew := int64(opts.ClockSkew / time.Second)
	if claims.Iss != opts.Issuer {
		return nil, fmt.Errorf("issuer mismatch")
	}
	if claims.Aud != opts.IntegrationID {
		return nil, fmt.Errorf("audience mismatch")
	}
	if claims.Room != opts.RoomID {
		return nil, fmt.Errorf("room mismatch")
	}
	if claims.Exp == 0 || nowUnix > claims.Exp+skew {
		return nil, fmt.Errorf("token expired")
	}
	if claims.Iat > nowUnix+skew {
		return nil, fmt.Errorf("token issue time is in the future")
	}
	if claims.Sub == "" {
		return nil, fmt.Errorf("token subject is missing")
	}
	return &claims, nil
}
