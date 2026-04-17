package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"log"
	"os"
	"strconv"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	// DataDir is the mounted volume root (default /data).
	DataDir string
	// PublicPort is the port the public server listens on (default 8080).
	PublicPort int
	// PrivatePort is the port the private server listens on (default 8081).
	PrivatePort int
	// DevMode relaxes signature verification (no Ed25519 key required) and skips
	// CF-Access checks when DevSkipAuth is also true. Disk writes still happen so
	// upload functionality can be verified locally. Enabled by --dev flag or GALLERY_DEV_MODE=true.
	DevMode bool
	// DevSkipAuth skips all CF-Access header checks in dev mode.
	// Enabled by GALLERY_DEV_SKIP_AUTH=true.
	DevSkipAuth bool
	// Ed25519PublicKey is used by the private server to verify request signatures.
	// Required in production mode. Set via GALLERY_ED25519_PUBLIC_KEY (base64-encoded).
	Ed25519PublicKey ed25519.PublicKey
}

// Load builds a Config from environment variables. devFlag should be set when
// the --dev CLI flag is present.
func Load(devFlag bool) *Config {
	cfg := &Config{
		DataDir:     envDefault("GALLERY_DATA_DIR", "/data"),
		PublicPort:  envInt("GALLERY_PUBLIC_PORT", 8080),
		PrivatePort: envInt("GALLERY_PRIVATE_PORT", 8081),
		DevMode:     devFlag || envBool("GALLERY_DEV_MODE"),
		DevSkipAuth: envBool("GALLERY_DEV_SKIP_AUTH"),
	}

	if !cfg.DevMode {
		keyB64 := os.Getenv("GALLERY_ED25519_PUBLIC_KEY")
		if keyB64 == "" {
			log.Fatal("GALLERY_ED25519_PUBLIC_KEY is required in production mode")
		}
		keyBytes, err := base64.StdEncoding.DecodeString(keyB64)
		if err != nil {
			log.Fatalf("invalid GALLERY_ED25519_PUBLIC_KEY: %v", err)
		}
		if len(keyBytes) != ed25519.PublicKeySize {
			log.Fatalf("GALLERY_ED25519_PUBLIC_KEY must be %d bytes (got %d)", ed25519.PublicKeySize, len(keyBytes))
		}
		cfg.Ed25519PublicKey = ed25519.PublicKey(keyBytes)
	}

	return cfg
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "true" || v == "1" || v == "yes"
}
