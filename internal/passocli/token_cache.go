package passocli

import (
	"context"
	"errors"
	"github.com/pyxcloud/pyxcloud-cli/internal/passoauth"
	"os"
	"sync"
	"time"
)

// cachedTokenAccess keeps credentials only in this command's memory. Serializing
// access avoids duplicate Keychain dialogs and refresh-token rotation races.
func cachedTokenAccess(store passoauth.Store, profile string, now func() time.Time, refresh func(context.Context, string) (passoauth.Token, error)) func(context.Context) (string, error) {
	var mu sync.Mutex
	var token passoauth.Token
	var loaded bool
	var loadErr error
	return func(ctx context.Context) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if env := os.Getenv("PASSO_ACCESS_TOKEN"); env != "" {
			return env, nil
		}
		mu.Lock()
		defer mu.Unlock()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !loaded {
			token, loadErr = loadTokenBeforeDeadline(ctx, store, profile)
			if ctx.Err() != nil {
				loadErr = passoauth.ContextPhase("credential_read", ctx.Err())
			}
			loaded = true
		}
		if loadErr != nil {
			return "", loadErr
		}
		if token.ExpiresAt.After(now().Add(30 * time.Second)) {
			return token.AccessToken, nil
		}
		if token.RefreshToken == "" {
			return "", errors.New("credentials unavailable")
		}
		fresh, err := refresh(ctx, token.RefreshToken)
		if err != nil {
			if ctx.Err() != nil {
				return "", passoauth.ContextPhase("refresh", ctx.Err())
			}
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", passoauth.ContextPhase("refresh", err)
		}
		if err = store.Save(profile, fresh); err != nil {
			loadErr = err
			return "", err
		}
		token = fresh
		return token.AccessToken, nil
	}
}

// Native credential reads may wait in Security.framework beyond the HTTP
// timeout. A late read is discarded and cannot refresh, save or use its token.
func loadTokenBeforeDeadline(ctx context.Context, store passoauth.Store, profile string) (passoauth.Token, error) {
	type result struct {
		token passoauth.Token
		err   error
	}
	done := make(chan result, 1)
	go func() {
		token, err := store.Load(profile)
		done <- result{token, err}
	}()
	select {
	case <-ctx.Done():
		return passoauth.Token{}, ctx.Err()
	case loaded := <-done:
		if err := ctx.Err(); err != nil {
			return passoauth.Token{}, err
		}
		return loaded.token, loaded.err
	}
}
