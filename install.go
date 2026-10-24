package main

// 게임 파일 받기: 마인크래프트 버전 정보·게임 jar·라이브러리·애셋·자바(모장 제공)·Fabric·보조 모드.
// 받은 파일은 sha1 로 확인하고, 이미 맞는 파일이 있으면 건너뜀.

import (
	"archive/zip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------- 진행 상황
type Status struct {
	Phase   string `json:"phase"` // idle | prepare | running | error
	Text    string `json:"text"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"`
	LogTail string `json:"logTail,omitempty"`
}

var (
	status   = Status{Phase: "idle"}
	statusMu sync.Mutex
	doneCnt  atomic.Int64
)

func setStatus(phase, text string, total int64) {
	statusMu.Lock()
	status.Phase, status.Text, status.Total, status.Error = phase, text, total, ""
	statusMu.Unlock()
	doneCnt.Store(0)
}

func getStatus() Status {
	statusMu.Lock()
	defer statusMu.Unlock()
	s := status
	s.Done = doneCnt.Load()
	return s
}

func busy() bool { s := getStatus(); return s.Phase == "prepare" }

// ---------------------------------------------------------------- 내려받기
type dlTask struct {
	URL    string
	Path   string
	Sha1   string
	Sha256 string // 1.2.6 서명 목록의 값 (서버 파일은 이걸로 확인)
	Size   int64
	Exec   bool
	// 크기만 맞으면 믿음 (애셋처럼 이름이 곧 해시인 것)
	TrustSize bool
}

var dlClient = &http.Client{Timeout: 10 * time.Minute}

func fileSha1(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha1.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func fileSha256(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func needDownload(t dlTask) bool {
	st, err := os.Stat(t.Path)
	if err != nil || st.IsDir() {
		return true
	}
	if t.Size > 0 && st.Size() != t.Size {
		return true
	}
	if t.Sha256 != "" {
		return !strings.EqualFold(fileSha256(t.Path), t.Sha256)
	}
	if t.TrustSize || t.Sha1 == "" {
		return false
	}
	return !strings.EqualFold(fileSha1(t.Path), t.Sha1)
}

func download(t dlTask) error {
	var last error
	for try := 0; try < 4; try++ {
		if try > 0 {
			time.Sleep(time.Duration(try) * time.Second)
		}
		if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
			return err
		}
		resp, err := dlClient.Get(t.URL)
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			last = fmt.Errorf("%s: HTTP %d", t.URL, resp.StatusCode)
			if resp.StatusCode == 404 {
				return last
			}
			continue
		}
		tmp := t.Path + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			resp.Body.Close()
			return err
		}
		h := sha1.New()
		h2 := sha256.New()
		_, err = io.Copy(io.MultiWriter(f, h, h2), resp.Body)
		resp.Body.Close()
		f.Close()
		if err != nil {
			last = err
			os.Remove(tmp)
			continue
		}
		if t.Sha256 != "" && !strings.EqualFold(hex.EncodeToString(h2.Sum(nil)), t.Sha256) {
			os.Remove(tmp)
			return fmt.Errorf("%s: 서명된 파일과 달라요 (바꿔치기됐을 수 있어 설치하지 않았어요)", filepath.Base(t.Path))
		}
		if t.Sha1 != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), t.Sha1) {
			last = fmt.Errorf("%s: 받은 파일이 깨졌어요 (sha1)", filepath.Base(t.Path))
			os.Remove(tmp)
			continue
		}
		_ = os.Remove(t.Path)
		if err := os.Rename(tmp, t.Path); err != nil {
			return err
		}
		if t.Exec {
			_ = os.Chmod(t.Path, 0o755)
		}
		return nil
	}
	return last
}

// 여러 파일을 동시에 받음 (진행 수 = doneCnt)
func downloadAll(label string, tasks []dlTask, workers int) error {
	var need []dlTask
	for _, t := range tasks {
		if needDownload(t) {
			need = append(need, t)
		} else if t.Exec {
			_ = os.Chmod(t.Path, 0o755)
		}
	}
	if len(need) == 0 {
		return nil
	}
	setStatus("prepare", label, int64(len(need)))
	ch := make(chan dlTask)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range ch {
				if err := download(t); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					logger.Printf("받기 실패 %s: %v", t.URL, err)
				}
				doneCnt.Add(1)
			}
		}()
	}
	for _, t := range need {
		ch <- t
	}
	close(ch)
	wg.Wait()
	return firstErr
}

func getJSON(url string, out any) error {
	resp, err := httpc.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// 파일로 받아 두고 (sha1 확인) JSON 으로 읽음. 인터넷이 안 되면 받아 둔 것을 씀
func cachedJSON(url, path, sha string, out any) error {
	t := dlTask{URL: url, Path: path, Sha1: sha}
	if needDownload(t) {
		if err := download(t); err != nil {
			if _, e2 := os.Stat(path); e2 != nil {
				return err
			}
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// ---------------------------------------------------------------- 서버가 알려 주는 것 (/launcher/manifest.json)
type ModFile struct {
	File string `json:"file"`
	Sha1 string `json:"sha1"`
	Size int64  `json:"size"`
}

// 편의성 모드 (서버 plugins/KoreaWar/mod/optional): 사람마다 런처 "모드" 화면에서 켜고 끔
type OptMod struct {
	File        string   `json:"file"`
	Sha1        string   `json:"sha1"`
	Size        int64    `json:"size"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Icon        string   `json:"icon,omitempty"`
	On          bool     `json:"on"` // 서버가 정한 처음 값
	Depends     []string `json:"depends"`
	Lib         bool     `json:"lib"`              // 라이브러리: 목록에 안 보이고 필요할 때만 같이 받음
	Detail      string   `json:"detail,omitempty"` // 누르면 보이는 자세한 설명
	Link        string   `json:"link,omitempty"`   // 모드 페이지 (https)
}

// 실제로 받을 편의성 모드 (id): 켠 것 + 그것들이 필요로 하는 것(줄줄이). neededBy[id] = 그걸 필요로 하는 켠 모드 이름들
func optActive(man *Manifest) (active map[string]bool, neededBy map[string][]string) {
	active, neededBy = map[string]bool{}, map[string][]string{}
	byID := map[string]OptMod{}
	for _, m := range man.Optional {
		byID[m.ID] = m
	}
	var queue []string
	for _, m := range man.Optional {
		if !m.Lib && optEnabled(m) {
			active[m.ID] = true
			queue = append(queue, m.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, d := range byID[id].Depends {
			if _, ok := byID[d]; !ok {
				continue
			}
			neededBy[d] = append(neededBy[d], byID[id].Name)
			if !active[d] {
				active[d] = true
				queue = append(queue, d)
			}
		}
	}
	return
}

func optEnabled(m OptMod) bool {
	c := getCfg()
	if v, ok := c.Mods[m.ID]; ok {
		return v
	}
	return m.On
}

type Manifest struct {
	Minecraft     string            `json:"minecraft"`
	FabricLoader  string            `json:"fabricLoader"`
	ClientID      string            `json:"clientId"`
	Server        string            `json:"server"`
	Mods          []ModFile         `json:"mods"`
	Optional      []OptMod          `json:"optional"`
	BlockedMods   []string          `json:"blockedMods"` // 서버가 막는 모드 id (미니맵 등)
	Launcher      string            `json:"launcher"`
	LauncherFiles map[string]string `json:"launcherFiles"`
	LauncherSha1  map[string]string `json:"launcherSha1"` // 자동 업데이트용 (파일 이름 -> sha1)
}

var (
	manifest   *Manifest
	manifestMu sync.Mutex
)

func filesBase() string {
	c := getCfg()
	return fmt.Sprintf("http://%s:%d", c.Host, c.FilesPort)
}

func cachedManifest() *Manifest {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	if manifest == nil {
		var m Manifest
		if b, err := os.ReadFile(filepath.Join(dataDir, "manifest.json")); err == nil && json.Unmarshal(b, &m) == nil {
			// 1.2.7 디스크에 남은 것은 다시 확인할 수 없으니 보안 값은 버림 (서버에서 새로 받으면 서명된 값으로 채워짐)
			m.Launcher, m.ClientID, m.Server, m.BlockedMods, m.Mods = "", "", "", nil, nil
			manifest = &m
		}
	}
	return manifest
}

func fetchManifest() (*Manifest, error) {
	var m Manifest
	if err := getJSON(filesBase()+"/launcher/manifest.json", &m); err != nil {
		if c := cachedManifest(); c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("서버에서 정보를 받지 못했어요 (서버가 꺼져 있거나 인터넷 연결 문제): %v", err)
	}
	if m.Minecraft == "" {
		m.Minecraft = "26.3"
	}
	// 1.2.7 버전 글자는 엄격히 (경로·주소에 쓰이므로)
	if !verRe.MatchString(m.Minecraft) {
		return nil, fmt.Errorf("서버가 알려 준 마인크래프트 버전이 이상해요")
	}
	if m.FabricLoader != "" && !verRe.MatchString(m.FabricLoader) {
		return nil, fmt.Errorf("서버가 알려 준 Fabric 버전이 이상해요")
	}
	applySigned(&m)
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(filepath.Join(dataDir, "manifest.json"), b, 0o644)
	manifestMu.Lock()
	manifest = &m
	manifestMu.Unlock()
	return &m, nil
}

func dirs(sub ...string) string { return filepath.Join(append([]string{dataDir}, sub...)...) }

// ---------------------------------------------------------------- 보조 모드 (서버의 plugins/KoreaWar/mod)
func gameDir() string { return dirs("game") }

// jar 안 fabric.mod.json 의 id
func jarModID(path string) string {
	z, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name != "fabric.mod.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		var j struct {
			ID string `json:"id"`
		}
		b, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
		_ = json.Unmarshal(b, &j)
		return j.ID
	}
	return ""
}

// 서버가 막는 모드(미니맵 등)를 mods 폴더에서 mods-blocked 로 옮김. 옮긴 파일 이름들
func moveBlockedMods(man *Manifest) []string {
	bad := map[string]bool{}
	for _, id := range man.BlockedMods {
		bad[strings.ToLower(id)] = true
	}
	var moved []string
	if len(bad) == 0 {
		return nil
	}
	modsDir := filepath.Join(gameDir(), "mods")
	es, _ := os.ReadDir(modsDir)
	for _, e := range es {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(n), ".jar") {
			continue
		}
		if id := jarModID(filepath.Join(modsDir, n)); bad[strings.ToLower(id)] {
			bd := filepath.Join(gameDir(), "mods-blocked")
			_ = os.MkdirAll(bd, 0o755)
			if os.Rename(filepath.Join(modsDir, n), filepath.Join(bd, n)) == nil {
				logger.Printf("막힌 모드 뺌: %s (%s)", n, id)
				moved = append(moved, n)
			}
		}
	}
	return moved
}

var lastBlocked []string

func syncMods(man *Manifest) error {
	modsDir := filepath.Join(gameDir(), "mods")
	_ = os.MkdirAll(modsDir, 0o755)
	lastBlocked = moveBlockedMods(man)
	signed, err := fetchSigned()
	if err != nil {
		return err
	}
	lastUnsigned = nil
	var tasks []dlTask
	want := map[string]bool{}
	for _, m := range man.Mods {
		if m.File == "" || strings.ContainsAny(m.File, `/\`) {
			continue
		}
		h := signed["mod/"+m.File]
		if h == "" {
			return fmt.Errorf("서버의 필수 모드 %s 에 서명이 없어서 받지 않았어요. 서버장이 kwsign 으로 서명해야 해요.", m.File)
		}
		want[m.File] = true
		tasks = append(tasks, dlTask{URL: filesBase() + "/mod/" + m.File, Path: filepath.Join(modsDir, m.File), Sha256: h, Size: m.Size})
	}
	active, _ := optActive(man)
	for _, m := range man.Optional {
		if m.File == "" || strings.ContainsAny(m.File, `/\`) || want[m.File] || !active[m.ID] {
			continue
		}
		h := signed["mod/optional/"+m.File]
		if h == "" {
			lastUnsigned = append(lastUnsigned, m.Name)
			logger.Printf("서명 없는 편의성 모드 건너뜀: %s", m.File)
			continue
		}
		want[m.File] = true
		tasks = append(tasks, dlTask{URL: filesBase() + "/mod/optional/" + m.File, Path: filepath.Join(modsDir, m.File), Sha256: h, Size: m.Size})
	}
	if err := downloadAll("보조 모드 업데이트 중", tasks, 4); err != nil {
		return fmt.Errorf("보조 모드를 받지 못했어요: %v", err)
	}
	// 예전에 런처가 넣은 것 중 이제 안 쓰는 것만 지움 (직접 넣은 모드는 그대로)
	managedPath := dirs("managed-mods.json")
	var old []string
	if b, err := os.ReadFile(managedPath); err == nil {
		_ = json.Unmarshal(b, &old)
	}
	for _, f := range old {
		if !want[f] {
			if os.Remove(filepath.Join(modsDir, f)) == nil {
				logger.Printf("예전 모드 지움: %s", f)
			}
		}
	}
	var now []string
	for f := range want {
		now = append(now, f)
	}
	b, _ := json.Marshal(now)
	_ = os.WriteFile(managedPath, b, 0o644)
	return nil
}

// 처음 켤 때: 한국어, 서버 목록(리소스팩 자동 수락)
func writeGameDefaults() {
	gd := gameDir()
	_ = os.MkdirAll(gd, 0o755)
	opt := filepath.Join(gd, "options.txt")
	if _, err := os.Stat(opt); err != nil {
		_ = os.WriteFile(opt, []byte("lang:ko_kr\nonboardAccessibility:false\nskipMultiplayerWarning:true\n"), 0o644)
	}
	sd := filepath.Join(gd, "servers.dat")
	if _, err := os.Stat(sd); err != nil {
		_ = os.WriteFile(sd, serversDat("KoreaWar", serverAddr()), 0o644)
	}
}

func serverAddr() string {
	c := getCfg()
	if m := cachedManifest(); m != nil && m.Server != "" {
		return m.Server
	}
	if c.GamePort == 25565 {
		return c.Host
	}
	return fmt.Sprintf("%s:%d", c.Host, c.GamePort)
}

// 압축 안 된 NBT: { servers: [ { name, ip, acceptTextures: 1b } ] }
func serversDat(name, ip string) []byte {
	var b []byte
	str := func(s string) { b = append(b, byte(len(s)>>8), byte(len(s))); b = append(b, s...) }
	b = append(b, 10)
	str("")
	b = append(b, 9)
	str("servers")
	b = append(b, 10, 0, 0, 0, 1)
	b = append(b, 8)
	str("name")
	str(name)
	b = append(b, 8)
	str("ip")
	str(ip)
	b = append(b, 1)
	str("acceptTextures")
	b = append(b, 1)
	b = append(b, 0) // 서버 하나 끝
	b = append(b, 0) // 루트 끝
	return b
}
