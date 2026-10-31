package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/nyk/support-portal/protocol"
)

// TURN credentials use coturn's "REST API" / use-auth-secret scheme: the
// username is "<unix-expiry>:<label>" and the password is
// base64(HMAC-SHA1(sharedSecret, username)). coturn is configured with the
// same shared secret and validates the HMAC itself, so we never store
// per-user TURN passwords. Credentials here are short-lived (minutes), just
// long enough to establish a session.
//
// turnConfig is built from server flags; an empty TURNHost disables TURN and
// the server hands out only STUN (or nothing), relying on direct P2P.
type turnConfig struct {
	Host   string // host:port of the TURN server, e.g. "turn.example.com:3478"
	Secret string // shared static-auth secret, must match coturn
	TLSURL string // optional turns: TLS TURN url, e.g. "turns:turn.example.com:5349"
	TTL    time.Duration
}

// iceServers builds the ICE server list handed to both peers for one session.
// label is embedded in the TURN username for traceability (e.g. the session
// id). Returns a STUN-only list if TURN is not configured.
func (c turnConfig) iceServers(label string) []protocol.ICEServer {
	if c.Host == "" {
		// Fall back to a public STUN server so P2P still has a chance; no relay.
		return []protocol.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}
	}
	ttl := c.TTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	expiry := time.Now().Add(ttl).Unix()
	username := strconv.FormatInt(expiry, 10) + ":" + label

	mac := hmac.New(sha1.New, []byte(c.Secret))
	mac.Write([]byte(username))
	credential := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	urls := []string{
		fmt.Sprintf("stun:%s", c.Host),
		fmt.Sprintf("turn:%s?transport=udp", c.Host),
		fmt.Sprintf("turn:%s?transport=tcp", c.Host),
	}
	if c.TLSURL != "" {
		urls = append(urls, c.TLSURL+"?transport=tcp")
	}
	return []protocol.ICEServer{{
		URLs:       urls,
		Username:   username,
		Credential: credential,
	}}
}
