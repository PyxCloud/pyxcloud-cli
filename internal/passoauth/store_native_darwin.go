//go:build darwin && cgo

package passoauth

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#cgo CFLAGS: -Wno-deprecated-declarations
#include <Security/Security.h>
#include <stdlib.h>
static OSStatus passoSave(const char *service, UInt32 serviceLen, const char *account, UInt32 accountLen, const void *data, UInt32 dataLen) {
    SecKeychainItemRef item = NULL;
    OSStatus status = SecKeychainFindGenericPassword(NULL, serviceLen, service, accountLen, account, NULL, NULL, &item);
    if (status == errSecItemNotFound) {
        return SecKeychainAddGenericPassword(NULL, serviceLen, service, accountLen, account, dataLen, data, NULL);
    }
    if (status != errSecSuccess) return status;
    status = SecKeychainItemModifyAttributesAndData(item, NULL, dataLen, data);
    CFRelease(item);
    return status;
}
static OSStatus passoLoad(const char *service, UInt32 serviceLen, const char *account, UInt32 accountLen, void **data, UInt32 *dataLen) {
    return SecKeychainFindGenericPassword(NULL, serviceLen, service, accountLen, account, dataLen, data, NULL);
}
static OSStatus passoDelete(const char *service, UInt32 serviceLen, const char *account, UInt32 accountLen) {
    SecKeychainItemRef item = NULL;
    OSStatus status = SecKeychainFindGenericPassword(NULL, serviceLen, service, accountLen, account, NULL, NULL, &item);
    if (status != errSecSuccess) return status;
    status = SecKeychainItemDelete(item);
    CFRelease(item);
    return status;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// nativeKeychainSave sends data directly to Security.framework. No subprocess,
// command argument, prompt-length limit, credential file or logging is involved.
func nativeKeychainSave(service, account string, data []byte) error {
	if len(data) == 0 || len(data) > 1024*1024 {
		return errors.New("invalid credential size")
	}
	svc := C.CString(service)
	defer C.free(unsafe.Pointer(svc))
	acct := C.CString(account)
	defer C.free(unsafe.Pointer(acct))
	payload := C.CBytes(data)
	defer C.free(payload)
	status := C.passoSave(svc, C.UInt32(len(service)), acct, C.UInt32(len(account)), payload, C.UInt32(len(data)))
	if status != C.errSecSuccess {
		return errors.New("could not save credentials to Keychain")
	}
	return nil
}

func nativeKeychainLoad(service, account string) ([]byte, error) {
	svc := C.CString(service)
	defer C.free(unsafe.Pointer(svc))
	acct := C.CString(account)
	defer C.free(unsafe.Pointer(acct))
	var payload unsafe.Pointer
	var length C.UInt32
	status := C.passoLoad(svc, C.UInt32(len(service)), acct, C.UInt32(len(account)), &payload, &length)
	if status != C.errSecSuccess {
		return nil, errors.New("could not load credentials from Keychain")
	}
	defer C.SecKeychainItemFreeContent(nil, payload)
	if length > 1024*1024 {
		return nil, errors.New("invalid credential size")
	}
	return C.GoBytes(payload, C.int(length)), nil
}
func nativeKeychainDelete(service, account string) error {
	svc := C.CString(service)
	defer C.free(unsafe.Pointer(svc))
	acct := C.CString(account)
	defer C.free(unsafe.Pointer(acct))
	if C.passoDelete(svc, C.UInt32(len(service)), acct, C.UInt32(len(account))) != C.errSecSuccess {
		return errors.New("could not delete credentials from Keychain")
	}
	return nil
}
