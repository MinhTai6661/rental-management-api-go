package main

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"rental-management/config"
	oauthadapter "rental-management/internal/adapter/oauth"
	identitypg "rental-management/internal/adapter/persistence/postgres/identity"
	redisstore "rental-management/internal/adapter/redis"
	usecase "rental-management/internal/usecase/identity"
)

// defaultLoginReturnPath is where the web app lands after a social sign-in that asked for nothing else.
const defaultLoginReturnPath = "dashboard"

// newSocial builds the social login use cases: the providers that have credentials, the state and the one-time
// codes in Redis, and the links in Postgres. Sessions and tokens are the ones of auth.
func newSocial(cfg config.Config, auth *usecase.Auth, pool *pgxpool.Pool, rdb *redis.Client) *usecase.Social {
	return newSocialWith(cfg, auth, pool, rdb, socialProviders(cfg))
}

// newSocialWith is newSocial with the providers given (tests register fakes that need no network).
func newSocialWith(cfg config.Config, auth *usecase.Auth, pool *pgxpool.Pool, rdb *redis.Client, providers []usecase.SocialProvider) *usecase.Social {
	return usecase.NewSocial(auth, usecase.SocialDeps{
		Providers: usecase.NewSocialRegistry(providers...),
		States:    redisstore.NewSocialStateStore(rdb, cfg.RedisKeyPrefix),
		Codes:     redisstore.NewLoginCodeStore(rdb, cfg.RedisKeyPrefix),
		Accounts:  identitypg.NewOAuthAccountRepository(pool),
		Users:     identitypg.NewUserRepository(pool),
	}, usecase.SocialOptions{
		StateTTL: cfg.SocialStateTTL, CodeTTL: cfg.SocialLoginCodeTTL, DefaultReturnPath: defaultLoginReturnPath,
	})
}

// socialProviders lists the providers that are switched on, in the order the web shows them: one with only half of
// its credentials never gets here (config refuses it at start-up).
func socialProviders(cfg config.Config) []usecase.SocialProvider {
	var providers []usecase.SocialProvider
	if cfg.GoogleEnabled() {
		providers = append(providers, oauthadapter.NewGoogle(oauthadapter.GoogleConfig{
			ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret, RedirectURL: cfg.GoogleRedirectURL,
		}))
	}
	if cfg.ZaloEnabled() {
		providers = append(providers, oauthadapter.NewZalo(oauthadapter.NewZaloClient(oauthadapter.ZaloClientConfig{
			AppID: cfg.ZaloAppID, AppSecret: cfg.ZaloAppSecret,
		}), cfg.ZaloRedirectURL))
	}
	return providers
}
