# Chegal Minecraft Launcher (KoreaWar Launcher)

A small, free, open-source **Minecraft: Java Edition** launcher for one private community server (*Korea War*, a nation-war server on a Korean-peninsula map).
Not affiliated with Mojang or Microsoft. No ads, no payments, no data collection.

![screenshot](docs/screenshot.png)

## What it does (1.3.0+)
Sign-in and game launch are handled by the **official Minecraft Launcher**. This app only prepares things:
1. **Install Fabric Loader** — writes the Fabric version JSON (`meta.fabricmc.net`) into the official launcher's `.minecraft/versions`; the official launcher downloads the game, libraries and Java itself.
2. **Keep the server's companion mods up to date** in a separate game folder (signed list, SHA-256 checked), so they never mix with the player's own `.minecraft/mods`.
3. **Create a "Korea War" installation** in the official launcher (`launcher_profiles.json`: game directory, memory) and open the official launcher.
4. **Auto-connect** — leaves a one-time note in the game folder that the companion mod reads on the title screen.

The app never sees Microsoft or Minecraft credentials. (Before 1.3.0 it signed in directly; that code has been removed.)

## Network endpoints used
| Host | Purpose |
|---|---|
| meta.fabricmc.net | Fabric Loader version JSON |
| our game server (port 8163) | signed mod list, mod files, launcher updates |

## Build
Go 1.24+, standard library only: `./build.sh 1.0.0` (Windows / macOS / Linux, x64 and arm64).
The UI is a local web page served on `127.0.0.1:25585` and opened in the default browser.

---

## 한국어
Korea War 서버 전용 마인크래프트 자바 에디션 런처예요. Fabric·보조 모드를 받고 업데이트한 뒤, 공식 마인크래프트 런처에 "Korea War" 설치 프로필을 만들어 열어 줘요.
로그인과 게임 실행은 공식 런처가 해서, 이 런처는 계정 정보를 전혀 다루지 않아요. 무료·광고 없음·오픈 소스 (MIT).

## License
MIT
