//go:build !windows

package main

import "errors"

// 맥·리눅스: 파일 권한(본인만 읽기, 0600)으로만 보호
const protPrefix = "KWDPAPI1:"
const protectAvailable = false

func protect(in []byte) ([]byte, error)   { return nil, errors.New("지원 안 함") }
func unprotect(in []byte) ([]byte, error) { return nil, errors.New("지원 안 함") }
