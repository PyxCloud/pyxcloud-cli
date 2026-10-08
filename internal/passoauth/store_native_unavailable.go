//go:build !darwin || !cgo

package passoauth

import "errors"

func nativeKeychainSave(service, account string, data []byte) error {
	return errors.New("native Keychain credential storage requires a macOS build with cgo")
}

func nativeKeychainLoad(service, account string) ([]byte, error) {
	return nil, errors.New("native Keychain storage unavailable")
}
func nativeKeychainDelete(service, account string) error {
	return errors.New("native Keychain storage unavailable")
}
