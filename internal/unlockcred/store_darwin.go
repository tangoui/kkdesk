//go:build darwin && cgo

package unlockcred

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

// The login keychain API also supports ad-hoc signed desktop builds.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static OSStatus query(const char *service, const char *account,
	const void *secret, UInt32 size, void **result, UInt32 *length) {
	SecKeychainRef keychain = NULL;
	OSStatus status = SecKeychainCopyDefault(&keychain);
	if (status != errSecSuccess) return status;
	UInt32 sl = (UInt32)strlen(service);
	UInt32 al = (UInt32)strlen(account);
	SecKeychainItemRef item = NULL;
	status = SecKeychainFindGenericPassword(keychain, sl, service, al, account, length, result,
		result ? NULL : &item);
	if (status == errSecItemNotFound && secret) {
		status = SecKeychainAddGenericPassword(keychain, sl, service, al, account, size, secret, NULL);
		if (status == errSecDuplicateItem) {
			status = SecKeychainFindGenericPassword(keychain, sl, service, al, account, NULL, NULL, &item);
		}
	}
	if (item) {
		status = secret ? SecKeychainItemModifyAttributesAndData(item, NULL, size, secret)
			: SecKeychainItemDelete(item);
		CFRelease(item);
	}
	CFRelease(keychain);
	return status;
}
static void free_password(void *bytes, UInt32 size) {
	if (!bytes) return;
	volatile unsigned char *p = bytes;
	for (UInt32 i = 0; i < size; i++) p[i] = 0;
	SecKeychainItemFreeContent(NULL, bytes);
}
#pragma clang diagnostic pop
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type keychainStore string

func nativeStore(service string) Store { return keychainStore(service) }

func (s keychainStore) query(account string, secret []byte, result *unsafe.Pointer, length *C.UInt32) error {
	service, user := C.CString(string(s)), C.CString(account)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(user))
	switch status := C.query(service, user, unsafe.Pointer(unsafe.SliceData(secret)), C.UInt32(len(secret)), result, length); status {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return ErrNotFound
	case C.errSecNotAvailable, C.errSecInteractionNotAllowed, C.errSecNoSuchKeychain, C.errAuthorizationInternal:
		return fmt.Errorf("%w: OSStatus %d", errStoreUnavailable, int(status))
	default:
		return fmt.Errorf("keychain error %d", int(status))
	}
}

func (s keychainStore) Read(account string) ([]byte, error) {
	var data unsafe.Pointer
	var size C.UInt32
	if err := s.query(account, nil, &data, &size); err != nil {
		return nil, err
	}
	defer C.free_password(data, size)
	return C.GoBytes(data, C.int(size)), nil
}

func (s keychainStore) Write(account string, secret []byte) error {
	if len(secret) == 0 {
		return fmt.Errorf("cannot store empty unlock credential")
	}
	return s.query(account, secret, nil, nil)
}

func (s keychainStore) Delete(account string) error { return s.query(account, nil, nil, nil) }
