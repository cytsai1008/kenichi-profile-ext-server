// Package auth verifies Cloudflare Access headers and Ed25519 request signatures.
package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// maxSkew is the maximum allowed clock difference between client and server.
// Must match SIGN_MAX_SKEW_SECONDS in galleryAssetShared.ts.
const maxSkew = 120 * time.Second

// nonceTTL is how long nonces are retained to detect replays.
const nonceTTL = 10 * time.Minute

// Verifier checks auth on private-server requests.
type Verifier struct {
	pubKey  ed25519.PublicKey
	devMode bool

	mu     sync.Mutex
	nonces map[string]time.Time
}

// NewVerifier creates a Verifier. In dev mode pubKey may be nil.
func NewVerifier(pubKey ed25519.PublicKey, devMode bool) *Verifier {
	v := &Verifier{
		pubKey:  pubKey,
		devMode: devMode,
		nonces:  make(map[string]time.Time),
	}
	go v.sweepNonces()
	return v
}

func (v *Verifier) sweepNonces() {
	t := time.NewTicker(nonceTTL)
	for range t.C {
		v.mu.Lock()
		cutoff := time.Now().Add(-nonceTTL)
		for nonce, seen := range v.nonces {
			if seen.Before(cutoff) {
				delete(v.nonces, nonce)
			}
		}
		v.mu.Unlock()
	}
}

// VerifySmallBody performs full signature verification for requests with a small,
// already-read body (GET, DELETE, PUT /_manifest/...).
// bodyBytes may be nil or empty for requests with no body.
func (v *Verifier) VerifySmallBody(r *http.Request, bodyBytes []byte) error {
	if v.devMode {
		return nil
	}
	if bodyBytes == nil {
		bodyBytes = []byte{}
	}
	sum := sha256.Sum256(bodyBytes)
	return v.verifySignature(r, hex.EncodeToString(sum[:]))
}

// VerifyUploadBody performs full signature verification after an upload body has been
// streamed and its SHA-256 computed. Call this after streaming to temp file.
func (v *Verifier) VerifyUploadBody(r *http.Request, computedHashHex string) error {
	if v.devMode {
		return nil
	}
	return v.verifySignature(r, computedHashHex)
}

// verifySignature checks the Ed25519 request signature and nonce.
// computedBodyHashHex is the SHA-256 hex of the request body already computed by the caller.
func (v *Verifier) verifySignature(r *http.Request, computedBodyHashHex string) error {
	timestampStr := r.Header.Get("x-timestamp")
	nonce := r.Header.Get("x-nonce")
	sentHashHex := r.Header.Get("x-content-sha256")
	sigB64 := r.Header.Get("x-signature")

	if timestampStr == "" || nonce == "" || sentHashHex == "" || sigB64 == "" {
		return fmt.Errorf("missing required signature headers")
	}

	// Validate timestamp window.
	tsUnix, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid x-timestamp: %w", err)
	}
	skew := time.Since(time.Unix(tsUnix, 0))
	if skew < -maxSkew || skew > maxSkew {
		return fmt.Errorf("timestamp outside allowed skew window (%v)", skew)
	}

	// The sent body hash must match what we actually computed.
	if sentHashHex != computedBodyHashHex {
		return fmt.Errorf("body hash mismatch")
	}

	// Reconstruct canonical payload. Must match buildSignedHeaders in gallery-explicit-sync.mjs.
	payload := r.Method + "\n" + r.URL.Path + "\n" + timestampStr + "\n" + nonce + "\n" + sentHashHex

	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("invalid x-signature encoding: %w", err)
	}

	if !ed25519.Verify(v.pubKey, []byte(payload), sig) {
		return fmt.Errorf("signature verification failed")
	}

	// Check and record nonce to prevent replays.
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, seen := v.nonces[nonce]; seen {
		return fmt.Errorf("nonce replay detected")
	}
	v.nonces[nonce] = time.Now()

	return nil
}
