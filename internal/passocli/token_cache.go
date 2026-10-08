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
			token, loadErr = store.Load(profile)
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
			return "", err
		}
		if err = store.Save(profile, fresh); err != nil {
			loadErr = err
			return "", err
		}
		token = fresh
		return token.AccessToken, nil
	}
}
