package livekit

// Fork (Agenda Maestros 4x4): a host join link that can be told apart from the one minted
// before it. SignRoomToken is deterministic - the same room, role and expiry give the same
// token - so after a booking passes to another person, a "fresh" host link for the new
// host would be byte-identical to the one the previous host was emailed, and a stored hash
// could not tell them apart. The fork's token carries one more field, a random nonce "n".
// VerifyRoomToken ignores unknown fields, so the format stays readable by the upstream
// code path unchanged: same payload.mac shape, same MAC, same room/role/expiry.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// roomTokenPayloadNonce is roomTokenPayload plus the fork's nonce.
type roomTokenPayloadNonce struct {
	R    string `json:"r"`
	E    int64  `json:"e"`
	Role string `json:"role,omitempty"`
	N    string `json:"n"` // random, only to make the token unique
}

// SignRoomTokenUnique is SignRoomToken with a random nonce in the payload: every call
// returns a different token for the same room, role and expiry.
func (c *Client) SignRoomTokenUnique(room, role string, exp time.Time) string {
	nonce := make([]byte, 12)
	_, _ = rand.Read(nonce)
	p, _ := json.Marshal(roomTokenPayloadNonce{R: room, E: exp.Unix(), Role: role, N: hex.EncodeToString(nonce)})
	payload := base64.RawURLEncoding.EncodeToString(p)
	return payload + "." + c.roomMAC(payload)
}

// BookingJoinURLUnique is BookingJoinURL over SignRoomTokenUnique (the handler stores the
// hash of its "t" parameter to recognise it later).
func (c *Client) BookingJoinURLUnique(baseURL, room, role string, exp time.Time) string {
	t := c.SignRoomTokenUnique(room, role, exp)
	return strings.TrimRight(baseURL, "/") + "/room/" + url.PathEscape(room) + "?t=" + url.QueryEscape(t)
}
