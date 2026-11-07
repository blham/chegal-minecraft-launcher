@echo off
chcp 65001 >nul
title Asia War Server
cd /d "%~dp0"
rem KW-START 5: 켤 때마다 GitHub 에서 아시아 워 최신 판을 확인해 plugins 에 넣고 켬 (서명·sha256 확인). Paper 가 없으면 Paper 도 받음.
rem          플러그인이 새 판(plugins\AsiaWar\.restart)이나 새 Paper(paper.jar.new)를 받아 두면 다시 켤 때 바꿔 끼움.
rem 직접 stop 하면 그대로 꺼짐. 메모리는 아래 -Xmx 숫자로.
:start
rem 3.8.7 이름 변경: 예전 이름의 플러그인 파일(KoreaWar-*.jar)을 AsiaWar.jar 로 (받아 둔 새 판도 같이)
if not exist "plugins\AsiaWar*.jar" for %%f in (plugins\KoreaWar-*.jar) do (
  if exist "plugins\update\%%~nxf" ren "plugins\update\%%~nxf" "AsiaWar.jar"
  ren "%%f" "AsiaWar.jar"
)
if exist "paper.jar.new" (
  del /q "paper.jar" 2>nul
  for %%f in (paper-*.jar) do del /q "%%f"
  ren "paper.jar.new" "paper.jar"
)
rem 4.0.7 최신 판 받기 (이 파일 맨 아래 프로그램을 임시 폴더에 꺼내 java 로 실행)
echo [Asia War] GitHub 에서 최신 판 확인 중...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$l=Get-Content -LiteralPath '%~f0' -Encoding UTF8; $i=[Array]::IndexOf($l,'//ASIAWAR-UPDATE'); [IO.File]::WriteAllLines((Join-Path $env:TEMP 'AsiaWarUpdate.java'), [string[]]$l[($i+1)..($l.Count-1)])"
java "%TEMP%\AsiaWarUpdate.java"
if errorlevel 1 if not exist "plugins\AsiaWar*.jar" (
  echo [Asia War] java 로 확인하지 못해 PowerShell 로 받습니다.
  powershell -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; [Net.ServicePointManager]::SecurityProtocol='Tls12'; $j=Invoke-RestMethod 'https://raw.githubusercontent.com/blham/chegal-minecraft-launcher/main/plugin/update.json'; if(-not $j.url.StartsWith('https://github.com/')){throw 'bad url'}; New-Item -ItemType Directory -Force plugins | Out-Null; $t='plugins\AsiaWar.jar.part'; Invoke-WebRequest $j.url -OutFile $t -UseBasicParsing; if((Get-Item $t).Length -ne $j.size -or (Get-FileHash $t -Algorithm SHA256).Hash -ne $j.sha256.ToUpper()){Remove-Item $t; throw 'sha256'}; Move-Item -Force $t 'plugins\AsiaWar.jar'; Write-Host ('[Asia War] AsiaWar '+$j.version+' 을(를) 넣었어요.')"
)
set JAR=paper.jar
if not exist "%JAR%" for %%f in (paper-*.jar) do set JAR=%%f
if not exist "%JAR%" (
  echo [Asia War] paper.jar 가 없어요. 인터넷 연결을 확인하고 다시 켜 주세요.
  pause
  exit /b
)
if not exist "eula.txt" (
  echo.
  echo [Asia War] 처음 켜는 서버예요. 마인크래프트 EULA https://aka.ms/MinecraftEULA 를 읽고 동의하면 아무 키나 누르세요.
  pause >nul
  echo eula=true> eula.txt
)
java -Xms4G -Xmx16G -jar %JAR% nogui
set RS=
if exist "plugins\AsiaWar\.restart" (del "plugins\AsiaWar\.restart" & set RS=1)
if exist "plugins\KoreaWar\.restart" (del "plugins\KoreaWar\.restart" & set RS=1)
rmdir "plugins\KoreaWar" 2>nul
if not defined RS goto end
echo.
echo [Asia War] 5초 뒤 다시 켭니다. 멈추려면 창을 닫으세요.
timeout /t 5 >nul
goto start
:end
echo.
echo 서버가 꺼졌어요.
pause
exit /b

rem ---------------------------------------------------------------- 아래는 최신 판 받는 프로그램 (java 가 읽음, 배치 파일은 여기까지 오지 않음)
//ASIAWAR-UPDATE
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.KeyFactory;
import java.security.MessageDigest;
import java.security.Signature;
import java.security.spec.X509EncodedKeySpec;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Base64;
import java.util.HexFormat;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.zip.ZipFile;

/** 아시아 워 start.bat 의 최신 판 받기 (KW-START 5). 실패해도 서버는 지금 있는 것으로 켜짐. */
public class AsiaWarUpdate {
    static final String PUB = "9QaY6jmmp+5wo2+Wm+/ybKpqDcshIb3fACyc1sLHMmE=";   // 플러그인 AutoUpdater 와 같은 확인 열쇠
    static final String BASE = "https://raw.githubusercontent.com/blham/chegal-minecraft-launcher/main/plugin/update";
    static final String MC = "26.3";
    static final HttpClient HTTP = HttpClient.newBuilder().followRedirects(HttpClient.Redirect.NORMAL).connectTimeout(Duration.ofSeconds(15)).build();

    static void say(String s) { System.out.println("[Asia War] " + s); }

    public static void main(String[] a) {
        try { paper(); } catch (Exception e) { say("Paper 받기 실패: " + e.getMessage()); }
        try { plugin(); } catch (Exception e) { say("최신 판 확인 실패, 지금 있는 플러그인으로 켭니다 (" + e.getMessage() + ")"); }
    }

    static void plugin() throws Exception {
        Path plugins = Path.of("plugins");
        Files.createDirectories(plugins);
        byte[] j = get(BASE + ".json", 64 * 1024), sig = get(BASE + ".sig", 4096);
        if (!verify(j, new String(sig, StandardCharsets.UTF_8))) { say("업데이트 정보의 서명이 맞지 않아요! 받지 않았어요."); return; }
        String js = new String(j, StandardCharsets.UTF_8);
        String type = str(js, "type"), ver = str(js, "version"), url = str(js, "url"), sha = str(js, "sha256");
        long size = Long.parseLong(num(js, "size"));
        if (!"koreawar-plugin".equals(type) || !url.startsWith("https://github.com/")) { say("업데이트 정보가 이상해요. 받지 않았어요."); return; }
        String cur = null;
        for (Path p : list(plugins)) { String v = version(p); if (v != null && (cur == null || newer(v, cur))) cur = v; }
        Path upd = plugins.resolve("update");
        if (cur != null) for (Path p : list(upd)) { String v = version(p); if (v != null && newer(v, cur)) cur = v; }   // Paper 가 켤 때 바꿔 끼울 판
        if (cur != null && !newer(ver, cur)) { say("최신 판이에요 (" + cur + ")"); return; }
        say((cur == null ? "플러그인이 없어서 " : "새 판이 나와서 (" + cur + " → " + ver + ") ") + "AsiaWar " + ver + " 받는 중… (" + size / 1024 / 1024 + "MB)");
        byte[] jar = get(url, size + 1);
        String got = HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(jar));
        if (jar.length != size || !got.equalsIgnoreCase(sha)) { say("받은 파일이 달라서 넣지 않았어요."); return; }
        Path tmp = plugins.resolve("AsiaWar.jar.part");
        Files.write(tmp, jar);
        for (Path p : list(plugins)) Files.deleteIfExists(p);
        for (Path p : list(upd)) Files.deleteIfExists(p);
        Files.move(tmp, plugins.resolve("AsiaWar.jar"), StandardCopyOption.REPLACE_EXISTING);
        say("AsiaWar " + ver + " 을(를) 넣었어요.");
    }

    /** paper.jar 가 아예 없을 때만 최신 안정판을 받음 (있으면 플러그인이 알아서 새 판으로 바꿈) */
    static void paper() throws Exception {
        if (Files.exists(Path.of("paper.jar"))) return;
        try (var ds = Files.newDirectoryStream(Path.of("."), "paper-*.jar")) { if (ds.iterator().hasNext()) return; }
        say("paper.jar 가 없어서 Paper " + MC + " 최신 안정판을 받습니다…");
        String js = new String(get("https://fill.papermc.io/v3/projects/paper/versions/" + MC + "/builds", 4 * 1024 * 1024), StandardCharsets.UTF_8);
        Matcher m = Pattern.compile("\"id\"\\s*:\\s*(\\d+)[^{}]*?\"channel\"\\s*:\\s*\"STABLE\"").matcher(js);
        int best = -1;
        while (m.find()) best = Math.max(best, Integer.parseInt(m.group(1)));
        if (best < 0) { say("Paper " + MC + " 안정판을 찾지 못했어요."); return; }
        String bj = new String(get("https://fill.papermc.io/v3/projects/paper/versions/" + MC + "/builds/" + best, 1024 * 1024), StandardCharsets.UTF_8);
        int d = bj.indexOf("\"server:default\"");
        String part = bj.substring(d);
        String url = str(part, "url"), sha = str(part, "sha256");
        if (!url.startsWith("https://fill-data.papermc.io/") && !url.startsWith("https://api.papermc.io/")) { say("Paper 주소가 이상해요."); return; }
        byte[] jar = get(url, 200L * 1024 * 1024);
        if (!HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(jar)).equalsIgnoreCase(sha)) { say("받은 Paper 파일이 달라서 넣지 않았어요."); return; }
        Files.write(Path.of("paper.jar"), jar);
        say("Paper " + MC + "-" + best + " 을(를) 넣었어요.");
    }

    static List<Path> list(Path dir) throws Exception {
        List<Path> out = new ArrayList<>();
        if (!Files.isDirectory(dir)) return out;
        try (var ds = Files.newDirectoryStream(dir, "AsiaWar*.jar")) { for (Path p : ds) out.add(p); }
        return out;
    }

    static String version(Path jar) {
        try (ZipFile z = new ZipFile(jar.toFile())) {
            var e = z.getEntry("plugin.yml");
            if (e == null) return null;
            String y = new String(z.getInputStream(e).readAllBytes(), StandardCharsets.UTF_8);
            Matcher m = Pattern.compile("(?m)^version:\\s*['\"]?([^'\"\\s]+)").matcher(y);
            return m.find() ? m.group(1) : null;
        } catch (Exception e) { return null; }
    }

    static boolean newer(String a, String b) {
        String[] x = a.split("[.\\-+]"), y = b.split("[.\\-+]");
        for (int i = 0; i < Math.max(x.length, y.length); i++) {
            int p = i < x.length ? n(x[i]) : 0, q = i < y.length ? n(y[i]) : 0;
            if (p != q) return p > q;
        }
        return false;
    }

    static int n(String s) { try { return Integer.parseInt(s.replaceAll("\\D", "")); } catch (Exception e) { return 0; } }

    static String str(String js, String k) {
        Matcher m = Pattern.compile("\"" + k + "\"\\s*:\\s*\"([^\"]*)\"").matcher(js);
        if (!m.find()) throw new IllegalStateException(k + " 없음");
        return m.group(1);
    }

    static String num(String js, String k) {
        Matcher m = Pattern.compile("\"" + k + "\"\\s*:\\s*(\\d+)").matcher(js);
        if (!m.find()) throw new IllegalStateException(k + " 없음");
        return m.group(1);
    }

    static byte[] get(String url, long max) throws Exception {
        var r = HTTP.send(HttpRequest.newBuilder(URI.create(url)).timeout(Duration.ofMinutes(10)).header("User-Agent", "AsiaWar-start").GET().build(),
                HttpResponse.BodyHandlers.ofInputStream());
        if (r.statusCode() != 200) throw new IllegalStateException("HTTP " + r.statusCode() + " " + url);
        try (var in = r.body()) {
            byte[] b = in.readNBytes((int) Math.min(max, Integer.MAX_VALUE - 8));
            if (in.read() != -1) throw new IllegalStateException("너무 큼");
            return b;
        }
    }

    static boolean verify(byte[] data, String sigB64) throws Exception {
        byte[] raw = Base64.getDecoder().decode(PUB), spki = new byte[44];
        System.arraycopy(HexFormat.of().parseHex("302a300506032b6570032100"), 0, spki, 0, 12);
        System.arraycopy(raw, 0, spki, 12, 32);
        var s = Signature.getInstance("Ed25519");
        s.initVerify(KeyFactory.getInstance("Ed25519").generatePublic(new X509EncodedKeySpec(spki)));
        s.update(data);
        return s.verify(Base64.getDecoder().decode(sigB64.trim()));
    }
}
