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
// The query targets the existing file-based generic-password record. Do not set
// kSecUseDataProtectionKeychain or change process-wide interaction policy.
static OSStatus passoLoad(const char *service, UInt32 serviceLen, const char *account, UInt32 accountLen, CFDataRef *data) {
    CFStringRef svc = CFStringCreateWithBytes(NULL, (const UInt8 *)service, serviceLen, kCFStringEncodingUTF8, false);
    CFStringRef acct = CFStringCreateWithBytes(NULL, (const UInt8 *)account, accountLen, kCFStringEncodingUTF8, false);
    if (!svc || !acct) {
        if (svc) CFRelease(svc);
        if (acct) CFRelease(acct);
        return errSecAllocate;
    }
    const void *keys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecReturnData, kSecMatchLimit, kSecUseAuthenticationUI};
    const void *values[] = {kSecClassGenericPassword, svc, acct, kCFBooleanTrue, kSecMatchLimitOne, kSecUseAuthenticationUIFail};
    CFDictionaryRef query = CFDictionaryCreate(NULL, keys, values, 6, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFRelease(svc);
    CFRelease(acct);
    if (!query) return errSecAllocate;
    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(query, &result);
    CFRelease(query);
    if (status != errSecSuccess) {
        if (result) CFRelease(result);
        return status;
    }
    if (!result || CFGetTypeID(result) != CFDataGetTypeID()) {
        if (result) CFRelease(result);
        return errSecInternalComponent;
    }
    *data = (CFDataRef)result;
    return errSecSuccess;
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
	var payload C.CFDataRef
	status := C.passoLoad(svc, C.UInt32(len(service)), acct, C.UInt32(len(account)), &payload)
	if status == C.errSecInteractionNotAllowed {
		return nil, ErrCredentialAccessRequired
	}
	if status != C.errSecSuccess {
		return nil, errors.New("could not load credentials from Keychain")
	}
	defer C.CFRelease(C.CFTypeRef(payload))
	length := C.CFDataGetLength(payload)
	if length < 0 || length > 1024*1024 {
		return nil, errors.New("invalid credential size")
	}
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(payload)), C.int(length)), nil
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
