//go:build windows

package main

// 1.2.6 로그인 정보 잠금 (윈도우 DPAPI): 이 윈도우 사용자 계정에서만 풀 수 있음.
// 파일을 다른 컴퓨터나 다른 사용자로 복사해 가면 못 씀.

import (
	"syscall"
	"unsafe"
)

const protPrefix = "KWDPAPI1:"
const protectAvailable = true

type dataBlob struct {
	cb   uint32
	data *byte
}

var (
	crypt32       = syscall.NewLazyDLL("crypt32.dll")
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	procProtect   = crypt32.NewProc("CryptProtectData")
	procUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree = kernel32.NewProc("LocalFree")
	entropy       = []byte("KoreaWarLauncher/account")
)

func blobOf(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{cb: uint32(len(b)), data: &b[0]}
}

func (b *dataBlob) bytes() []byte {
	out := make([]byte, b.cb)
	copy(out, unsafe.Slice(b.data, b.cb))
	return out
}

func protect(in []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procProtect.Call(uintptr(unsafe.Pointer(blobOf(in))), 0, uintptr(unsafe.Pointer(blobOf(entropy))), 0, 0, 0x1 /*UI_FORBIDDEN*/, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return out.bytes(), nil
}

func unprotect(in []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procUnprotect.Call(uintptr(unsafe.Pointer(blobOf(in))), 0, uintptr(unsafe.Pointer(blobOf(entropy))), 0, 0, 0x1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return out.bytes(), nil
}
