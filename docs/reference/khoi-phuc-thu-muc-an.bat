@echo off
chcp 65001 >nul
title Khoi phuc thu muc bi an (Hidden / System)

:: ================= TU DONG XIN QUYEN ADMIN =================
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo Dang xin quyen Administrator... Bam YES o hop thoai UAC.
    powershell -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
    exit /b
)

setlocal enabledelayedexpansion
color 0A

:ASK_DRIVE
echo.
echo ============================================================
set "drive="
set /p "drive=Nhap o dia can quet (vd: E) roi Enter  (Q = thoat): "
if /i "!drive!"=="Q" exit /b
if not defined drive goto ASK_DRIVE
set "drive=!drive:~0,1!"
set "root=!drive!:\"
if not exist "!root!" (
    echo [Loi] Khong tim thay o !root!
    goto ASK_DRIVE
)

echo.
echo === Cac thu muc bi AN tren !root! ===
set /a n=0
for /f "delims=" %%D in ('dir /b /a:hd "!root!" 2^>nul') do (
    set /a n+=1
    set "item[!n!]=%%D"
    echo    !n!.  %%D
)
if !n!==0 (
    echo    ^(Khong co thu muc an nao^)
    goto ASK_DRIVE
)

echo.
set "pick="
set /p "pick=Chon SO thu muc can khoi phuc  (Enter = quet lai): "
if not defined pick goto ASK_DRIVE
set "target=!item[%pick%]!"
if not defined target (
    echo Lua chon khong hop le.
    goto ASK_DRIVE
)
set "full=!root!!target!"

echo.
echo === KIEM TRA AN TOAN: WinRE that cua may dang o dau ===
reagentc /info
echo.
echo Chuan bi khoi phuc:  "!full!"
echo Hay chac chan day KHONG phai la thu muc Recovery cua phan vung WinRE that.
set "ok="
set /p "ok=Go Y de tiep tuc (phim khac = huy): "
if /i not "!ok!"=="Y" (
    echo Da huy.
    goto ASK_DRIVE
)

echo.
echo [1/3] Gianh quyen so huu...
takeown /f "!full!" /r /d y >nul
echo [2/3] Cap quyen cho Administrators...
icacls "!full!" /grant *S-1-5-32-544:F /t /c >nul
echo [3/3] Go thuoc tinh An + He thong...
attrib -h -s "!full!" /s /d

echo.
echo ================= XONG! =================
echo Thuoc tinh hien tai:
attrib "!full!"
echo Da khoi phuc: !full!
echo Mo thu muc trong File Explorer de kiem tra.
echo.
goto ASK_DRIVE
