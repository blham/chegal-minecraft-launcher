@echo off
chcp 65001 >nul
title Korea War Server
cd /d "%~dp0"
rem KW-START 3: 플러그인이 새 판(plugins\KoreaWar\.restart)이나 새 Paper(paper.jar.new)를 받아 두면 다시 켤 때 바꿔 끼움
rem 직접 stop 하면 그대로 꺼짐. 메모리는 아래 -Xmx 숫자로.
:start
if exist "paper.jar.new" (
  del /q "paper.jar" 2>nul
  for %%f in (paper-*.jar) do del /q "%%f"
  ren "paper.jar.new" "paper.jar"
)
set JAR=paper.jar
if not exist "%JAR%" for %%f in (paper-*.jar) do set JAR=%%f
if not exist "eula.txt" (
  echo.
  echo [Korea War] 처음 켜는 서버예요. 마인크래프트 EULA https://aka.ms/MinecraftEULA 를 읽고 동의하면 아무 키나 누르세요.
  pause >nul
  echo eula=true> eula.txt
)
java -Xms4G -Xmx16G -jar %JAR% nogui
if not exist "plugins\KoreaWar\.restart" goto end
del "plugins\KoreaWar\.restart"
echo.
echo [Korea War] 5초 뒤 다시 켭니다. 멈추려면 창을 닫으세요.
timeout /t 5 >nul
goto start
:end
echo.
echo 서버가 꺼졌어요.
pause
