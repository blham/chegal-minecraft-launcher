package main

// 마이크로소프트 로그인 -> Xbox Live -> XSTS -> 마인크래프트 로그인 -> 프로필.
// Azure 앱(공개 클라이언트, 리디렉션 http://localhost:25585/auth) 의 client id 로 PKCE 인증 코드 흐름.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Account struct {
	MsRefresh string `json:"msRefresh"`
	McToken   string `json:"mcToken"`
	McExpires int64  `json:"mcExpires"`
	Name      string `json:"name"`
	UUID      string `json:"uuid"`
	Xuid      string `json:"xuid"`
	ClientID  string `json:"clientId"`
}

var (
	account   *Account
	accMu     sync.Mutex
	pending   = map[string]string{} // state -> code_verifier
	pendMu    sync.Mutex
	authError string
)

const msAuthorize = "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize"
const msToken = "https://login.microsoftonline.com/consumers/oauth2/v2.0/token"
const msScope = "XboxLive.signin offline_access"

func redirectURI() string { return fmt.Sprintf("http://localhost:%d/auth", uiPort) }

var httpc = &http.Client{Timeout: 60 * time.Second}

func accountPath() string { return filepath.Join(dataDir, "account.json") }

func loadAccount() {
	b, err := os.ReadFile(accountPath())
	if err != nil {
		return
	}
	var a Account
	if json.Unmarshal(b, &a) == nil && a.Name != "" {
		account = &a
	}
}

func saveAccount() {
	accMu.Lock()
	defer accMu.Unlock()
	if account == nil {
		_ = os.Remove(accountPath())
		return
	}
	b, _ := json.MarshalIndent(account, "", "  ")
	_ = os.WriteFile(accountPath(), b, 0o600)
}

func currentAccount() *Account {
	accMu.Lock()
	defer accMu.Unlock()
	if account == nil {
		return nil
	}
	a := *account
	return &a
}

// clientID: 내 설정 > 서버가 알려 준 것 > 빌드에 넣은 것
func clientID() string {
	if c := getCfg().ClientID; c != "" {
		return c
	}
	if m := cachedManifest(); m != nil && m.ClientID != "" {
		return m.ClientID
	}
	return defaultClientID
}

func randString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// 로그인 주소 (브라우저로 엶)
func beginLogin() (string, error) {
	if _, err := fetchManifest(); err != nil {
		logger.Printf("서버 정보 못 받음: %v", err)
	}
	cid := clientID()
	if cid == "" {
		return "", errors.New("로그인용 앱 ID(client id)가 아직 없어요. 서버장이 서버 config.yml 의 launcher.client-id 를 채워야 해요.")
	}
	verifier := randString(48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randString(16)
	pendMu.Lock()
	pending[state] = verifier
	pendMu.Unlock()
	q := url.Values{}
	q.Set("client_id", cid)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI())
	q.Set("response_mode", "query")
	q.Set("scope", msScope)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "select_account")
	return msAuthorize + "?" + q.Encode(), nil
}

func handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back := func(msg string) {
		authError = msg
		http.Redirect(w, r, "/", http.StatusFound)
	}
	if e := q.Get("error"); e != "" {
		logger.Printf("로그인 취소/실패: %s %s", e, q.Get("error_description"))
		back("로그인이 취소되었거나 실패했어요: " + q.Get("error_description"))
		return
	}
	pendMu.Lock()
	verifier, ok := pending[q.Get("state")]
	delete(pending, q.Get("state"))
	pendMu.Unlock()
	if !ok {
		back("로그인 확인 값이 맞지 않아요. 다시 로그인해 주세요.")
		return
	}
	form := url.Values{}
	form.Set("client_id", clientID())
	form.Set("grant_type", "authorization_code")
	form.Set("code", q.Get("code"))
	form.Set("redirect_uri", redirectURI())
	form.Set("code_verifier", verifier)
	form.Set("scope", msScope)
	tok, err := msTokenRequest(form)
	if err != nil {
		back(err.Error())
		return
	}
	if err := finishLogin(tok); err != nil {
		back(err.Error())
		return
	}
	authError = ""
	http.Redirect(w, r, "/", http.StatusFound)
}

type msTok struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func msTokenRequest(form url.Values) (*msTok, error) {
	resp, err := httpc.PostForm(msToken, form)
	if err != nil {
		return nil, fmt.Errorf("마이크로소프트 로그인 서버에 연결하지 못했어요: %v", err)
	}
	defer resp.Body.Close()
	var t msTok
	_ = json.NewDecoder(resp.Body).Decode(&t)
	if t.AccessToken == "" {
		logger.Printf("MS 토큰 실패: %s %s", t.Error, t.ErrorDesc)
		return nil, fmt.Errorf("마이크로소프트 로그인 실패 (%s)", t.Error)
	}
	return &t, nil
}

func postJSON(u string, body any, out any, hdr map[string]string) (int, []byte, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", u, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, raw, nil
}

type xblResp struct {
	Token         string `json:"Token"`
	DisplayClaims struct {
		Xui []map[string]string `json:"xui"`
	} `json:"DisplayClaims"`
	XErr int64 `json:"XErr"`
}

// MS 토큰으로 마인크래프트 토큰·프로필까지 받아 계정에 저장
func finishLogin(t *msTok) error {
	var xbl xblResp
	st, raw, err := postJSON("https://user.auth.xboxlive.com/user/authenticate", map[string]any{
		"Properties":   map[string]any{"AuthMethod": "RPS", "SiteName": "user.auth.xboxlive.com", "RpsTicket": "d=" + t.AccessToken},
		"RelyingParty": "http://auth.xboxlive.com", "TokenType": "JWT",
	}, &xbl, nil)
	if err != nil || xbl.Token == "" {
		logger.Printf("XBL 실패 %d %s %v", st, raw, err)
		return errors.New("Xbox Live 로그인에 실패했어요. 잠시 뒤 다시 해 주세요.")
	}
	uhs := ""
	if len(xbl.DisplayClaims.Xui) > 0 {
		uhs = xbl.DisplayClaims.Xui[0]["uhs"]
	}
	xsts := func(rp string) (*xblResp, error) {
		var x xblResp
		st, raw, err := postJSON("https://xsts.auth.xboxlive.com/xsts/authorize", map[string]any{
			"Properties":   map[string]any{"SandboxId": "RETAIL", "UserTokens": []string{xbl.Token}},
			"RelyingParty": rp, "TokenType": "JWT",
		}, &x, nil)
		if err != nil {
			return nil, err
		}
		if x.Token == "" {
			logger.Printf("XSTS 실패 %d %s", st, raw)
			switch x.XErr {
			case 2148916233:
				return nil, errors.New("이 마이크로소프트 계정엔 Xbox 프로필이 없어요. minecraft.net 에서 한 번 로그인해 프로필을 만든 뒤 다시 해 주세요.")
			case 2148916235:
				return nil, errors.New("이 나라에선 Xbox Live 를 쓸 수 없어요.")
			case 2148916236, 2148916237:
				return nil, errors.New("성인 인증이 필요한 계정이에요 (Xbox 페이지에서 확인).")
			case 2148916238:
				return nil, errors.New("미성년자 계정이에요. 보호자가 Microsoft 가족 그룹에 추가해야 로그인할 수 있어요.")
			}
			return nil, fmt.Errorf("Xbox 인증 실패 (%d)", x.XErr)
		}
		return &x, nil
	}
	mcx, err := xsts("rp://api.minecraftservices.com/")
	if err != nil {
		return err
	}
	xuid := "0"
	if x2, err := xsts("http://xboxlive.com"); err == nil && len(x2.DisplayClaims.Xui) > 0 && x2.DisplayClaims.Xui[0]["xid"] != "" {
		xuid = x2.DisplayClaims.Xui[0]["xid"]
	}
	var mc struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	st, raw, err = postJSON("https://api.minecraftservices.com/authentication/login_with_xbox",
		map[string]string{"identityToken": "XBL3.0 x=" + uhs + ";" + mcx.Token}, &mc, nil)
	if err != nil || mc.AccessToken == "" {
		logger.Printf("MC 로그인 실패 %d %s %v", st, raw, err)
		if st == 403 || strings.Contains(string(raw), "Invalid app registration") {
			return errors.New("이 런처의 로그인 앱이 아직 Mojang 승인을 받지 못했어요 (Invalid app registration). 승인이 나면 바로 로그인돼요.")
		}
		return errors.New("마인크래프트 로그인에 실패했어요. 잠시 뒤 다시 해 주세요.")
	}
	req, _ := http.NewRequest("GET", "https://api.minecraftservices.com/minecraft/profile", nil)
	req.Header.Set("Authorization", "Bearer "+mc.AccessToken)
	resp, err := httpc.Do(req)
	if err != nil {
		return errors.New("마인크래프트 프로필을 받지 못했어요.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return errors.New("이 계정엔 마인크래프트 자바 에디션이 없어요 (게임을 산 계정으로 로그인해 주세요).")
	}
	var prof struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&prof)
	if prof.ID == "" {
		return errors.New("마인크래프트 프로필이 비어 있어요. minecraft.net 에서 닉네임을 정한 뒤 다시 해 주세요.")
	}
	accMu.Lock()
	refresh := t.RefreshToken
	if refresh == "" && account != nil {
		refresh = account.MsRefresh
	}
	account = &Account{MsRefresh: refresh, McToken: mc.AccessToken, McExpires: time.Now().Unix() + mc.ExpiresIn, Name: prof.Name, UUID: prof.ID, Xuid: xuid, ClientID: clientID()}
	accMu.Unlock()
	saveAccount()
	logger.Printf("로그인: %s", prof.Name)
	return nil
}

// 게임을 켜기 전에 토큰이 곧 끝나면 새로 받음
func ensureFreshToken() error {
	a := currentAccount()
	if a == nil {
		return errors.New("먼저 로그인해 주세요.")
	}
	if time.Now().Unix() < a.McExpires-600 {
		return nil
	}
	if a.MsRefresh == "" {
		return errors.New("로그인이 끝났어요. 다시 로그인해 주세요.")
	}
	form := url.Values{}
	cid := a.ClientID
	if cid == "" {
		cid = clientID()
	}
	form.Set("client_id", cid)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", a.MsRefresh)
	form.Set("scope", msScope)
	t, err := msTokenRequest(form)
	if err != nil {
		return errors.New("로그인이 만료됐어요. 다시 로그인해 주세요.")
	}
	return finishLogin(t)
}

func logout() {
	accMu.Lock()
	account = nil
	accMu.Unlock()
	saveAccount()
}
