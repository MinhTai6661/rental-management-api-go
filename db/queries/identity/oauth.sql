-- Queries of social login: the provider accounts that open accounts here, and the account created or confirmed by them.

-- name: GetOAuthAccountByProvider :one
SELECT user_id, provider, provider_user_id, linked_at, unlinked_at
FROM oauth_accounts WHERE provider = @provider AND provider_user_id = @provider_user_id;

-- name: ListOAuthAccountsOfUser :many
SELECT user_id, provider, provider_user_id, linked_at, unlinked_at
FROM oauth_accounts WHERE user_id = $1 ORDER BY linked_at, provider;

-- name: ListOAuthAccountsOfUserForUpdate :many
-- Locks the rows so two requests changing the sign-in methods of one account run one after the other.
SELECT user_id, provider, provider_user_id, linked_at, unlinked_at
FROM oauth_accounts WHERE user_id = $1 ORDER BY linked_at, provider FOR UPDATE;

-- name: CreateOAuthAccount :exec
INSERT INTO oauth_accounts (user_id, provider, provider_user_id, linked_at, created_at, updated_at)
VALUES (@user_id, @provider, @provider_user_id, @now, @now, @now);

-- name: RelinkOAuthAccount :exec
UPDATE oauth_accounts SET unlinked_at = NULL, linked_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE user_id = @user_id AND provider = @provider;

-- name: ReplaceOAuthAccount :exec
UPDATE oauth_accounts
SET provider_user_id = @provider_user_id, unlinked_at = NULL, linked_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE user_id = @user_id AND provider = @provider;

-- name: UnlinkOAuthAccount :exec
UPDATE oauth_accounts SET unlinked_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE user_id = @user_id AND provider = @provider AND unlinked_at IS NULL;

-- name: ConfirmEmailFromProvider :exec
-- The provider proved the address: it is verified, and a password set while it was unverified no longer counts.
UPDATE users
SET email_verified_at = COALESCE(email_verified_at, @now::timestamptz), password_hash = NULL, updated_at = @now::timestamptz
WHERE id = @id;
