//go:build dev

package main

import "os"

// 개발용 (빌드 태그 dev 에만 들어감): KW_DEV_OFFLINE=이름 이면 오프라인 계정으로 (오프라인 모드 테스트 서버 전용)
func init() {
	if n := os.Getenv("KW_DEV_OFFLINE"); n != "" {
		devOffline = n
	}
}
