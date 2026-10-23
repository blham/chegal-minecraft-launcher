package main

// 게임 파일 받기: 마인크래프트 버전 정보·게임 jar·라이브러리·애셋·자바(모장 제공)·Fabric·보조 모드.
// 받은 파일은 sha1 로 확인하고, 이미 맞는 파일이 있으면 건너뜀.

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	URL  string
	Path string
	Sha1 string
	Size int64
	Exec bool
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

func needDownload(t dlTask) bool {
	st, err := os.Stat(t.Path)
	if err != nil || st.IsDir() {
		return true
	}
	if t.Size > 0 && st.Size() != t.Size {
		return true
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
		_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
		resp.Body.Close()
		f.Close()
		if err != nil {
			last = err
			os.Remove(tmp)
			continue
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
type Manifest struct {
	Minecraft     string    `json:"minecraft"`
	FabricLoader  string    `json:"fabricLoader"`
	ClientID      string    `json:"clientId"`
	Server        string    `json:"server"`
	Mods          []ModFile `json:"mods"`
	Launcher      string    `json:"launcher"`
	LauncherFiles map[string]string `json:"launcherFiles"`
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
		return nil, fmt.Errorf("서버(%s)에서 정보를 받지 못했어요: %v", filesBase(), err)
	}
	if m.Minecraft == "" {
		m.Minecraft = "26.3"
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(filepath.Join(dataDir, "manifest.json"), b, 0o644)
	manifestMu.Lock()
	manifest = &m
	manifestMu.Unlock()
	return &m, nil
}

// ---------------------------------------------------------------- 버전 JSON
type Rule struct {
	Action string `json:"action"`
	OS     *struct {
		Name    string `json:"name"`
		Arch    string `json:"arch"`
		VersionRange *struct {
			Min string `json:"min"`
			Max string `json:"max"`
		} `json:"versionRange"`
	} `json:"os"`
	Features map[string]bool `json:"features"`
}

type Artifact struct {
	Path string `json:"path"`
	URL  string `json:"url"`
	Sha1 string `json:"sha1"`
	Size int64  `json:"size"`
	ID   string `json:"id"`
}

type Library struct {
	Name      string `json:"name"`
	URL       string `json:"url"` // Fabric: 메이븐 주소
	Sha1      string `json:"sha1"`
	Size      int64  `json:"size"`
	Downloads *struct {
		Artifact    *Artifact            `json:"artifact"`
		Classifiers map[string]*Artifact `json:"classifiers"`
	} `json:"downloads"`
	Natives map[string]string `json:"natives"`
	Rules   []Rule            `json:"rules"`
}

type VersionJSON struct {
	ID           string `json:"id"`
	InheritsFrom string `json:"inheritsFrom"`
	MainClass    string `json:"mainClass"`
	Type         string `json:"type"`
	Arguments    struct {
		Game       []json.RawMessage `json:"game"`
		JVM        []json.RawMessage `json:"jvm"`
		DefaultJVM []json.RawMessage `json:"default-user-jvm"`
	} `json:"arguments"`
	AssetIndex struct {
		ID   string `json:"id"`
		URL  string `json:"url"`
		Sha1 string `json:"sha1"`
	} `json:"assetIndex"`
	Downloads struct {
		Client Artifact `json:"client"`
	} `json:"downloads"`
	JavaVersion struct {
		Component    string `json:"component"`
		MajorVersion int    `json:"majorVersion"`
	} `json:"javaVersion"`
	Logging struct {
		Client struct {
			Argument string   `json:"argument"`
			File     Artifact `json:"file"`
		} `json:"client"`
	} `json:"logging"`
	Libraries []Library `json:"libraries"`
}

func osName() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "osx"
	}
	return "linux"
}

// 규칙: 맞는 규칙의 마지막 action (규칙이 있으면 기본은 안 됨)
func rulesAllow(rules []Rule, feats map[string]bool) bool {
	if len(rules) == 0 {
		return true
	}
	ok := false
	for _, r := range rules {
		match := true
		if r.OS != nil {
			if r.OS.Name != "" && r.OS.Name != osName() {
				match = false
			}
			if r.OS.Arch != "" {
				a := runtime.GOARCH
				if !(r.OS.Arch == "x86" && a == "386") && r.OS.Arch != a {
					match = false
				}
			}
			if r.OS.VersionRange != nil && r.OS.VersionRange.Max != "" && r.OS.VersionRange.Min == "" {
				match = false // 아주 옛 윈도용 (10.0.17134 이전) 은 안 씀
			}
		}
		for k, v := range r.Features {
			if feats[k] != v {
				match = false
			}
		}
		if match {
			ok = r.Action == "allow"
		}
	}
	return ok
}

// "group:artifact:version[:classifier]" -> 메이븐 경로
func mavenPath(name string) string {
	p := strings.Split(name, ":")
	if len(p) < 3 {
		return ""
	}
	g := strings.ReplaceAll(p[0], ".", "/")
	file := p[1] + "-" + p[2]
	if len(p) > 3 {
		file += "-" + p[3]
	}
	return g + "/" + p[1] + "/" + p[2] + "/" + file + ".jar"
}

// 같은 라이브러리(그룹:이름[:분류])의 다른 버전이 겹치면 자식(Fabric) 것을 씀
func libKey(name string) string {
	p := strings.Split(name, ":")
	if len(p) >= 4 {
		return p[0] + ":" + p[1] + ":" + p[3]
	}
	if len(p) >= 2 {
		return p[0] + ":" + p[1]
	}
	return name
}

func mergeVersions(child, parent *VersionJSON) *VersionJSON {
	v := *parent
	v.ID = child.ID
	if child.MainClass != "" {
		v.MainClass = child.MainClass
	}
	seen := map[string]bool{}
	var libs []Library
	for _, l := range child.Libraries {
		seen[libKey(l.Name)] = true
		libs = append(libs, l)
	}
	for _, l := range parent.Libraries {
		if !seen[libKey(l.Name)] {
			libs = append(libs, l)
		}
	}
	v.Libraries = libs
	v.Arguments.Game = append(append([]json.RawMessage{}, parent.Arguments.Game...), child.Arguments.Game...)
	v.Arguments.JVM = append(append([]json.RawMessage{}, parent.Arguments.JVM...), child.Arguments.JVM...)
	return &v
}

// ---------------------------------------------------------------- 준비 (받기·업데이트)
type Prepared struct {
	Version   *VersionJSON
	ClientJar string
	Classpath []string
	JavaPath  string
	Natives   string
	LogConfig string
	Manifest  *Manifest
}

func dirs(sub ...string) string { return filepath.Join(append([]string{dataDir}, sub...)...) }

func prepare() (*Prepared, error) {
	setStatus("prepare", "서버 정보 확인 중", 0)
	man, err := fetchManifest()
	if err != nil {
		return nil, err
	}
	mcv := man.Minecraft
	setStatus("prepare", "마인크래프트 "+mcv+" 정보 받는 중", 0)
	var vm struct {
		Versions []struct {
			ID   string `json:"id"`
			URL  string `json:"url"`
			Sha1 string `json:"sha1"`
		} `json:"versions"`
	}
	vmPath := dirs("versions", "version_manifest_v2.json")
	if err := getJSON("https://piston-meta.mojang.com/mc/game/version_manifest_v2.json", &vm); err == nil {
		b, _ := json.Marshal(vm)
		_ = os.MkdirAll(filepath.Dir(vmPath), 0o755)
		_ = os.WriteFile(vmPath, b, 0o644)
	} else if b, e2 := os.ReadFile(vmPath); e2 == nil {
		_ = json.Unmarshal(b, &vm)
	} else {
		return nil, fmt.Errorf("모장 서버에 연결하지 못했어요: %v", err)
	}
	var vurl, vsha string
	for _, v := range vm.Versions {
		if v.ID == mcv {
			vurl, vsha = v.URL, v.Sha1
		}
	}
	if vurl == "" {
		return nil, fmt.Errorf("마인크래프트 %s 버전을 찾지 못했어요", mcv)
	}
	var base VersionJSON
	if err := cachedJSON(vurl, dirs("versions", mcv, mcv+".json"), vsha, &base); err != nil {
		return nil, fmt.Errorf("버전 정보를 받지 못했어요: %v", err)
	}
	ver := &base
	if man.FabricLoader != "" {
		var fab VersionJSON
		fid := "fabric-loader-" + man.FabricLoader + "-" + mcv
		fpath := dirs("versions", fid, fid+".json")
		furl := "https://meta.fabricmc.net/v2/versions/loader/" + mcv + "/" + man.FabricLoader + "/profile/json"
		if _, err := os.Stat(fpath); err != nil {
			if err := download(dlTask{URL: furl, Path: fpath}); err != nil {
				return nil, fmt.Errorf("Fabric 정보를 받지 못했어요: %v", err)
			}
		}
		b, _ := os.ReadFile(fpath)
		if err := json.Unmarshal(b, &fab); err != nil {
			return nil, err
		}
		ver = mergeVersions(&fab, &base)
	}

	// 게임 jar + 라이브러리
	feats := map[string]bool{}
	var tasks []dlTask
	clientJar := dirs("versions", mcv, mcv+".jar")
	tasks = append(tasks, dlTask{URL: base.Downloads.Client.URL, Path: clientJar, Sha1: base.Downloads.Client.Sha1, Size: base.Downloads.Client.Size})
	var cp []string
	natives := dirs("natives", ver.ID)
	type extract struct{ jar string }
	var toExtract []string
	for _, l := range ver.Libraries {
		if !rulesAllow(l.Rules, feats) {
			continue
		}
		if l.Downloads != nil && l.Downloads.Artifact != nil && l.Downloads.Artifact.URL != "" {
			a := l.Downloads.Artifact
			p := dirs("libraries", filepath.FromSlash(a.Path))
			tasks = append(tasks, dlTask{URL: a.URL, Path: p, Sha1: a.Sha1, Size: a.Size})
			cp = append(cp, p)
		} else if l.Downloads == nil || l.Downloads.Artifact == nil {
			mp := mavenPath(l.Name)
			if mp == "" {
				continue
			}
			base := l.URL
			if base == "" {
				base = "https://libraries.minecraft.net/"
			}
			if !strings.HasSuffix(base, "/") {
				base += "/"
			}
			p := dirs("libraries", filepath.FromSlash(mp))
			tasks = append(tasks, dlTask{URL: base + mp, Path: p, Sha1: l.Sha1, Size: l.Size})
			cp = append(cp, p)
		}
		// 옛 방식 네이티브 (분류 jar 를 풀어 씀)
		if l.Natives != nil && l.Downloads != nil {
			if cls, ok := l.Natives[osName()]; ok {
				cls = strings.ReplaceAll(cls, "${arch}", map[bool]string{true: "64", false: "32"}[strings.HasSuffix(runtime.GOARCH, "64")])
				if a := l.Downloads.Classifiers[cls]; a != nil {
					p := dirs("libraries", filepath.FromSlash(a.Path))
					tasks = append(tasks, dlTask{URL: a.URL, Path: p, Sha1: a.Sha1, Size: a.Size})
					toExtract = append(toExtract, p)
				}
			}
		}
	}
	if err := downloadAll("게임 파일 받는 중", tasks, 8); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(natives, 0o755)
	for _, j := range toExtract {
		_ = unzipNatives(j, natives)
	}

	// 애셋
	setStatus("prepare", "애셋 목록 받는 중", 0)
	var idx struct {
		Objects map[string]struct {
			Hash string `json:"hash"`
			Size int64  `json:"size"`
		} `json:"objects"`
	}
	if err := cachedJSON(ver.AssetIndex.URL, dirs("assets", "indexes", ver.AssetIndex.ID+".json"), ver.AssetIndex.Sha1, &idx); err != nil {
		return nil, fmt.Errorf("애셋 목록을 받지 못했어요: %v", err)
	}
	seenHash := map[string]bool{}
	var atasks []dlTask
	for _, o := range idx.Objects {
		if seenHash[o.Hash] {
			continue
		}
		seenHash[o.Hash] = true
		atasks = append(atasks, dlTask{URL: "https://resources.download.minecraft.net/" + o.Hash[:2] + "/" + o.Hash,
			Path: dirs("assets", "objects", o.Hash[:2], o.Hash), Sha1: o.Hash, Size: o.Size, TrustSize: true})
	}
	if err := downloadAll("소리·언어 파일 받는 중", atasks, 16); err != nil {
		return nil, err
	}
	logCfg := ""
	if ver.Logging.Client.File.URL != "" {
		logCfg = dirs("assets", "log_configs", ver.Logging.Client.File.ID)
		_ = download(dlTask{URL: ver.Logging.Client.File.URL, Path: logCfg, Sha1: ver.Logging.Client.File.Sha1})
	}

	// 자바
	javaPath, err := ensureJava(ver.JavaVersion.Component)
	if err != nil {
		return nil, err
	}

	// 보조 모드
	if err := syncMods(man); err != nil {
		return nil, err
	}
	writeGameDefaults()

	cp = append(cp, clientJar)
	return &Prepared{Version: ver, ClientJar: clientJar, Classpath: cp, JavaPath: javaPath, Natives: natives, LogConfig: logCfg, Manifest: man}, nil
}

// ---------------------------------------------------------------- 자바 (모장이 게임용으로 주는 런타임)
func javaPlatform() string {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "windows-arm64"
		}
		if runtime.GOARCH == "386" {
			return "windows-x86"
		}
		return "windows-x64"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac-os-arm64"
		}
		return "mac-os"
	}
	if runtime.GOARCH == "386" {
		return "linux-i386"
	}
	return "linux"
}

func ensureJava(component string) (string, error) {
	if component == "" {
		component = "java-runtime-epsilon"
	}
	root := dirs("runtime", component, javaPlatform())
	marker := filepath.Join(root, ".kw-ok")
	javaExe := func() string {
		cands := []string{filepath.Join(root, "bin", "javaw.exe"), filepath.Join(root, "bin", "java.exe"),
			filepath.Join(root, "jre.bundle", "Contents", "Home", "bin", "java"), filepath.Join(root, "bin", "java")}
		for _, c := range cands {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return ""
	}
	setStatus("prepare", "자바 확인 중", 0)
	var all map[string]map[string][]struct {
		Manifest struct {
			URL  string `json:"url"`
			Sha1 string `json:"sha1"`
		} `json:"manifest"`
		Version struct {
			Name string `json:"name"`
		} `json:"version"`
	}
	if err := getJSON("https://launchermeta.mojang.com/v1/products/java-runtime/2ec0cc96c44e5a76b9c8b7c39df7210883d12871/all.json", &all); err != nil {
		if j := javaExe(); j != "" {
			return j, nil // 인터넷이 안 되면 받아 둔 것
		}
		return "", fmt.Errorf("자바 정보를 받지 못했어요: %v", err)
	}
	list := all[javaPlatform()][component]
	if len(list) == 0 {
		return "", fmt.Errorf("이 컴퓨터(%s)용 자바(%s)가 없어요", javaPlatform(), component)
	}
	mf := list[0].Manifest
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == mf.Sha1 {
		if j := javaExe(); j != "" {
			return j, nil
		}
	}
	var files struct {
		Files map[string]struct {
			Type       string `json:"type"`
			Executable bool   `json:"executable"`
			Target     string `json:"target"`
			Downloads  struct {
				Raw Artifact `json:"raw"`
			} `json:"downloads"`
		} `json:"files"`
	}
	if err := getJSON(mf.URL, &files); err != nil {
		return "", fmt.Errorf("자바 파일 목록을 받지 못했어요: %v", err)
	}
	var tasks []dlTask
	for p, f := range files.Files {
		full := filepath.Join(root, filepath.FromSlash(p))
		switch f.Type {
		case "directory":
			_ = os.MkdirAll(full, 0o755)
		case "file":
			tasks = append(tasks, dlTask{URL: f.Downloads.Raw.URL, Path: full, Sha1: f.Downloads.Raw.Sha1, Size: f.Downloads.Raw.Size, Exec: f.Executable})
		}
	}
	if err := downloadAll("자바 받는 중 ("+list[0].Version.Name+")", tasks, 12); err != nil {
		return "", err
	}
	for p, f := range files.Files { // 링크 (맥·리눅스)
		if f.Type == "link" && runtime.GOOS != "windows" {
			full := filepath.Join(root, filepath.FromSlash(p))
			_ = os.Remove(full)
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.Symlink(f.Target, full)
		}
	}
	j := javaExe()
	if j == "" {
		return "", errors.New("자바를 받았지만 실행 파일을 찾지 못했어요")
	}
	_ = os.WriteFile(marker, []byte(mf.Sha1), 0o644)
	return j, nil
}

// ---------------------------------------------------------------- 보조 모드 (서버의 plugins/KoreanWar/mod)
func gameDir() string { return dirs("game") }

func syncMods(man *Manifest) error {
	modsDir := filepath.Join(gameDir(), "mods")
	_ = os.MkdirAll(modsDir, 0o755)
	var tasks []dlTask
	want := map[string]bool{}
	for _, m := range man.Mods {
		if m.File == "" || strings.ContainsAny(m.File, `/\`) {
			continue
		}
		want[m.File] = true
		tasks = append(tasks, dlTask{URL: filesBase() + "/mod/" + m.File, Path: filepath.Join(modsDir, m.File), Sha1: m.Sha1, Size: m.Size})
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
		_ = os.WriteFile(sd, serversDat("KoreanWar", serverAddr()), 0o644)
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
