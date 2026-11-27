package unlockcred

import (
	"bytes"
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credGeneric      = 1
	credLocalMachine = 2
)

var (
	advapi     = windows.NewLazySystemDLL("advapi32.dll")
	credRead   = advapi.NewProc("CredReadW")
	credWrite  = advapi.NewProc("CredWriteW")
	credDelete = advapi.NewProc("CredDeleteW")
	credFree   = advapi.NewProc("CredFree")
)

type credential struct {
	flags, kind    uint32
	target         *uint16
	comment        *uint16
	lastWritten    windows.Filetime
	size           uint32
	blob           *byte
	persist, count uint32
	attrs          unsafe.Pointer
	alias          *uint16
	username       *uint16
}

type credentialStore string

func nativeStore(service string) Store { return credentialStore(service) }

func (s credentialStore) target(account string) (*uint16, error) {
	return windows.UTF16PtrFromString(string(s) + "/" + account)
}

func credentialError(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_NOT_FOUND):
		return ErrNotFound
	case errors.Is(err, windows.ERROR_NO_SUCH_LOGON_SESSION):
		return errStoreUnavailable
	default:
		return err
	}
}

func (s credentialStore) Read(account string) ([]byte, error) {
	target, err := s.target(account)
	if err != nil {
		return nil, err
	}
	var data *credential
	ok, _, err := credRead.Call(uintptr(unsafe.Pointer(target)), credGeneric, 0, uintptr(unsafe.Pointer(&data)))
	if ok == 0 {
		return nil, credentialError(err)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(data)))
	secret := unsafe.Slice(data.blob, data.size)
	defer clear(secret)
	return bytes.Clone(secret), nil
}

func (s credentialStore) Write(account string, secret []byte) error {
	target, err := s.target(account)
	if err != nil {
		return err
	}
	if len(secret) == 0 || len(secret) > 2560 {
		return errors.New("invalid unlock credential length")
	}
	data := credential{
		kind: credGeneric, target: target, size: uint32(len(secret)),
		blob: unsafe.SliceData(secret), persist: credLocalMachine,
	}
	ok, _, err := credWrite.Call(uintptr(unsafe.Pointer(&data)), 0)
	runtime.KeepAlive(secret)
	if ok == 0 {
		return credentialError(err)
	}
	return nil
}

func (s credentialStore) Delete(account string) error {
	target, err := s.target(account)
	if err != nil {
		return err
	}
	ok, _, err := credDelete.Call(uintptr(unsafe.Pointer(target)), credGeneric, 0)
	if ok == 0 {
		return credentialError(err)
	}
	return nil
}
