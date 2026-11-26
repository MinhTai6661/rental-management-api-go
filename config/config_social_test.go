package config

import (
	"strings"
	"testing"
	"time"
)

func TestSocialProvidersAreOffByDefault(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GoogleEnabled() || cfg.ZaloEnabled() {
		t.Error("a provider without credentials must be off")
	}
	if cfg.SocialStateTTL != 10*time.Minute || cfg.SocialLoginCodeTTL != 60*time.Second {
		t.Errorf("unexpected TTLs: %s %s", cfg.SocialStateTTL, cfg.SocialLoginCodeTTL)
	}
}

func TestSocialProviderIsOnWithIdAndSecret(t *testing.T) {
	cfg, err := parse(map[string]string{
		"GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "s", "GOOGLE_REDIRECT_URL": "http://localhost:8090/v1/auth/google/callback",
		"ZALO_APP_ID": "123", "ZALO_APP_SECRET": "z", "ZALO_REDIRECT_URL": "https://api.example.vn/v1/auth/zalo/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GoogleEnabled() || !cfg.ZaloEnabled() {
		t.Error("both providers have credentials")
	}
}

func TestSocialProviderMisconfigurationIsRefused(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"id without secret", map[string]string{"GOOGLE_CLIENT_ID": "id"}, "GOOGLE"},
		{"secret without id", map[string]string{"ZALO_APP_SECRET": "s"}, "ZALO"},
		{"enabled without redirect", map[string]string{"GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "s"}, "GOOGLE_REDIRECT_URL"},
		{"redirect is not http", map[string]string{"ZALO_APP_ID": "1", "ZALO_APP_SECRET": "s", "ZALO_REDIRECT_URL": "ftp://x"}, "ZALO_REDIRECT_URL"},
		{"state ttl", map[string]string{"SOCIAL_STATE_TTL": "0s"}, "SOCIAL_STATE_TTL"},
		{"code ttl", map[string]string{"SOCIAL_LOGIN_CODE_TTL": "-1s"}, "SOCIAL_LOGIN_CODE_TTL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(tc.env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error naming %s, got %v", tc.want, err)
			}
			for _, secret := range tc.env {
				if secret == "s" && strings.Contains(err.Error(), "=s") {
					t.Errorf("the error must not repeat a secret: %v", err)
				}
			}
		})
	}
}
