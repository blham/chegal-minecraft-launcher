package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 다른 런처(공식 런처·CurseForge·Prism 등)용: 서버 필수 모드(보조 모드 + Fabric API)를 zip 하나로 묶어 다운로드 폴더에 저장
func exportMods() (string, error) {
	man, err := fetchManifest()
	if err != nil {
		return "", err
	}
	if _, err := fetchSignedDoc(); err != nil { // 서명 문제면 그 이유를 그대로
		return "", err
	}
	if len(man.Mods) == 0 {
		return "", fmt.Errorf("서버에 필수 모드가 없어요")
	}
	tmp := dirs("cache", "export")
	_ = os.MkdirAll(tmp, 0o755)
	signed, err := fetchSigned()
	if err != nil {
		return "", err
	}
	var tasks []dlTask
	for _, m := range man.Mods {
		if m.File == "" || strings.ContainsAny(m.File, `/\`) {
			continue
		}
		if signed["mod/"+m.File] == "" {
			return "", fmt.Errorf("%s 에 서명이 없어요. 서버장이 kwsign 으로 서명해야 해요", m.File)
		}
		tasks = append(tasks, dlTask{URL: filesBase() + "/mod/" + m.File, Path: filepath.Join(tmp, m.File), Sha256: signed["mod/"+m.File], Size: m.Size})
	}
	if err := downloadAll("모드 묶는 중", tasks, 4); err != nil {
		return "", fmt.Errorf("모드를 받지 못했어요: %v", err)
	}
	home, _ := os.UserHomeDir()
	outDir := filepath.Join(home, "Downloads")
	if st, err := os.Stat(outDir); err != nil || !st.IsDir() {
		outDir = home
	}
	ver := "mods"
	for _, m := range man.Mods {
		if strings.HasPrefix(m.File, "koreawar-mod-") {
			ver = strings.TrimSuffix(strings.TrimPrefix(m.File, "koreawar-mod-"), ".jar")
		}
	}
	if !verRe.MatchString(ver) { // 1.2.7 파일 이름에 들어가는 글자는 엄격히 (다운로드 폴더 밖으로 못 나가게)
		ver = "mods"
	}
	mc := man.Minecraft
	if !verRe.MatchString(mc) {
		mc = "unknown"
	}
	out := filepath.Join(outDir, fmt.Sprintf("KoreaWar-mods-%s-mc%s.zip", ver, mc))
	if filepath.Dir(out) != filepath.Clean(outDir) {
		return "", fmt.Errorf("파일 이름이 이상해요")
	}
	f, err := os.Create(out)
	if err != nil {
		return "", fmt.Errorf("파일을 만들지 못했어요: %v", err)
	}
	defer f.Close()
	z := zip.NewWriter(f)
	for _, t := range tasks {
		w, err := z.Create("mods/" + filepath.Base(t.Path))
		if err != nil {
			return "", err
		}
		src, err := os.Open(t.Path)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return "", err
		}
	}
	fl := man.FabricLoader
	if fl == "" {
		fl = "최신"
	}
	nl := "\n"
	if runtime.GOOS == "windows" {
		nl = "\r\n"
	}
	readme := strings.Join([]string{
		"Korea War 서버 필수 모드 (다른 런처용)",
		"",
		"1. 마인크래프트 " + man.Minecraft + " 에 Fabric Loader " + fl + " 를 설치하세요.",
		"   - 공식 런처: https://fabricmc.net/use/installer/ 에서 설치기를 받아 버전 " + man.Minecraft + " 로 설치",
		"   - Prism·CurseForge·Modrinth 앱: 새 인스턴스를 마인크래프트 " + man.Minecraft + ", 로더 Fabric 으로 만들기",
		"2. 이 zip 안 mods 폴더의 .jar 를 그 게임의 mods 폴더에 넣으세요.",
		"3. 서버 주소: " + serverAddr(),
		"",
		"★ 중요: 보조 모드가 바뀔 때마다 이 파일을 새로 받아서 mods 폴더의 예전 koreawar-mod 를 지우고 바꿔 넣어야 해요.",
		"   (버전이 안 맞으면 모션이 이상하게 보여요. Korea War 런처로 켜면 자동으로 바뀌어요)",
		"미니맵·지도 모드는 서버에서 막혀 있어요 (넣고 들어오면 튕겨요).",
	}, nl)
	if w, err := z.Create("읽어 주세요.txt"); err == nil {
		_, _ = w.Write([]byte("\xef\xbb\xbf" + readme))
	}
	if err := z.Close(); err != nil {
		return "", err
	}
	_ = os.RemoveAll(tmp)
	cfgMu.Lock()
	cfg.Exported = ver
	cfgMu.Unlock()
	saveConfig()
	setStatus("idle", "다른 런처용 모드를 저장했어요: "+out, 0)
	return out, nil
}

// 지금 서버의 보조 모드 버전 (koreawar-mod-<버전>.jar)
func currentModVer(man *Manifest) string {
	if man == nil {
		return ""
	}
	for _, m := range man.Mods {
		if strings.HasPrefix(m.File, "koreawar-mod-") {
			return strings.TrimSuffix(strings.TrimPrefix(m.File, "koreawar-mod-"), ".jar")
		}
	}
	return ""
}
