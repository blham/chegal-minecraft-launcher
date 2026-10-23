package main

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	gameCmd   *exec.Cmd
	gameMu    sync.Mutex
	gameStart time.Time
)

func gameRunning() bool {
	gameMu.Lock()
	defer gameMu.Unlock()
	return gameCmd != nil
}

func unzipNatives(jar, dest string) error {
	r, err := zip.OpenReader(jar)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() || strings.HasPrefix(f.Name, "META-INF") {
			continue
		}
		name := filepath.Base(f.Name)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		out, err := os.Create(filepath.Join(dest, name))
		if err == nil {
			_, _ = io.Copy(out, rc)
			out.Close()
		}
		rc.Close()
	}
	return nil
}

var placeholder = regexp.MustCompile(`\$\{([a-zA-Z_]+)\}`)

func subst(s string, vars map[string]string) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		k := m[2 : len(m)-1]
		if v, ok := vars[k]; ok {
			return v
		}
		return m
	})
}

// 버전 JSON 의 arguments 항목: 글자 하나, 또는 {rules, value(글자 또는 목록)}
func expandArgs(raw []json.RawMessage, feats map[string]bool, vars map[string]string) []string {
	var out []string
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			out = append(out, subst(s, vars))
			continue
		}
		var obj struct {
			Rules []Rule          `json:"rules"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(r, &obj) != nil || !rulesAllow(obj.Rules, feats) {
			continue
		}
		var one string
		if json.Unmarshal(obj.Value, &one) == nil {
			out = append(out, subst(one, vars))
			continue
		}
		var many []string
		if json.Unmarshal(obj.Value, &many) == nil {
			for _, m := range many {
				out = append(out, subst(m, vars))
			}
		}
	}
	return out
}

func launchGame(p *Prepared) error {
	a := currentAccount()
	if a == nil {
		return fmt.Errorf("먼저 로그인해 주세요")
	}
	c := getCfg()
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	gd := gameDir()
	vars := map[string]string{
		"auth_player_name":     a.Name,
		"version_name":         p.Version.ID,
		"game_directory":       gd,
		"assets_root":          dirs("assets"),
		"assets_index_name":    p.Version.AssetIndex.ID,
		"auth_uuid":            a.UUID,
		"auth_access_token":    a.McToken,
		"clientid":             a.ClientID,
		"auth_xuid":            a.Xuid,
		"user_type":            "msa",
		"version_type":         "release",
		"natives_directory":    p.Natives,
		"launcher_name":        "KoreaWarLauncher",
		"launcher_version":     LauncherVersion,
		"classpath":            strings.Join(p.Classpath, sep),
		"classpath_separator":  sep,
		"library_directory":    dirs("libraries"),
		"quickPlayMultiplayer": serverAddr(),
		"path":                 p.LogConfig,
	}
	feats := map[string]bool{"is_quick_play_multiplayer": c.AutoJoin}
	for _, d := range []string{"java", "jna", "lwjgl", "netty"} {
		_ = os.MkdirAll(filepath.Join(p.Natives, d), 0o755)
	}
	// 기본 JVM 옵션 (메모리는 런처 설정으로)
	var jvm []string
	for _, s := range expandArgs(p.Version.Arguments.DefaultJVM, feats, vars) {
		if strings.HasPrefix(s, "-Xmx") || strings.HasPrefix(s, "-Xms") || strings.HasPrefix(s, "-XX:+AlwaysPreTouch") {
			continue
		}
		jvm = append(jvm, s)
	}
	ms := c.MemoryMB / 2
	if ms > 2048 {
		ms = 2048
	}
	jvm = append([]string{fmt.Sprintf("-Xmx%dM", c.MemoryMB), fmt.Sprintf("-Xms%dM", ms)}, jvm...)
	if p.LogConfig != "" && p.Version.Logging.Client.Argument != "" {
		jvm = append(jvm, subst(p.Version.Logging.Client.Argument, vars))
	}
	jvmMain := expandArgs(p.Version.Arguments.JVM, feats, vars)
	game := expandArgs(p.Version.Arguments.Game, feats, vars)
	if c.AutoJoin && !contains(game, "--quickPlayMultiplayer") {
		game = append(game, "--quickPlayMultiplayer", serverAddr())
	}
	args := append(append(append(jvm, jvmMain...), p.Version.MainClass), game...)

	cmd := exec.Command(p.JavaPath, args...)
	cmd.Dir = gd
	detach(cmd)
	logPath := dirs("logs", "game.log")
	lf, _ := os.Create(logPath)
	if lf != nil {
		cmd.Stdout = lf
		cmd.Stderr = lf
	}
	safe := make([]string, len(args))
	for i, s := range args {
		safe[i] = s
		if s == a.McToken {
			safe[i] = "(토큰)"
		}
	}
	logger.Printf("실행: %s %s", p.JavaPath, strings.Join(safe, " "))
	if err := cmd.Start(); err != nil {
		if lf != nil {
			lf.Close()
		}
		return fmt.Errorf("게임을 켜지 못했어요: %v", err)
	}
	gameMu.Lock()
	gameCmd = cmd
	gameStart = time.Now()
	gameMu.Unlock()
	if len(lastBlocked) > 0 {
		setStatus("running", "미니맵·지도 모드는 이 서버에서 못 써서 빼 두었어요 (mods-blocked 폴더): "+strings.Join(lastBlocked, ", "), 0)
	} else if len(lastUnsigned) > 0 {
		setStatus("running", "서명이 없어 안 받은 편의성 모드가 있어요 (서버장에게 알려 주세요): "+strings.Join(lastUnsigned, ", "), 0)
	} else {
		setStatus("running", a.Name+" 로 게임이 켜졌어요. 서버로 바로 접속해요.", 0)
	}
	go func() {
		err := cmd.Wait()
		if lf != nil {
			lf.Close()
		}
		ran := time.Since(gameStart)
		gameMu.Lock()
		gameCmd = nil
		gameMu.Unlock()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		logger.Printf("게임 끝 (코드 %d, %s) %v", code, ran.Round(time.Second), err)
		if code != 0 && ran < 2*time.Minute {
			statusMu.Lock()
			status = Status{Phase: "error", Text: "게임이 바로 꺼졌어요", Error: fmt.Sprintf("게임이 켜지다가 꺼졌어요 (코드 %d). 아래 기록을 서버장에게 보내 주세요.", code), LogTail: tail(logPath, 40)}
			statusMu.Unlock()
		} else {
			setStatus("idle", "게임이 끝났어요", 0)
		}
		gameEnded()
	}()
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func tail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// 게임 켜기 버튼: 토큰 확인 -> 받기·업데이트 -> 실행
var playMu sync.Mutex

func play() {
	if !playMu.TryLock() {
		return
	}
	defer playMu.Unlock()
	if gameRunning() {
		return
	}
	fail := func(err error) {
		logger.Printf("실패: %v", err)
		statusMu.Lock()
		status = Status{Phase: "error", Text: "준비하지 못했어요", Error: err.Error()}
		statusMu.Unlock()
	}
	setStatus("prepare", "로그인 확인 중", 0)
	if err := ensureFreshToken(); err != nil {
		fail(err)
		return
	}
	p, err := prepare()
	if err != nil {
		fail(err)
		return
	}
	setStatus("prepare", "게임 켜는 중", 0)
	if err := launchGame(p); err != nil {
		fail(err)
	}
}
