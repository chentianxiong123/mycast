#!/usr/bin/env bash
# mycast 安卓壳构建（零 Gradle/AGP，纯 SDK 工具链）：
#   GOOS=android 交叉编译服务端（默认 arm64-v8a，参数 x86_64 / both）→ assets/<abi> 内嵌
#   → aapt2 link → javac → d8 → zipalign → apksigner
# 产物：android/dist/mycast[-<abi>].apk
set -euo pipefail
cd "$(dirname "$0")"

ABI="${1:-arm64-v8a}"
case "$ABI" in
  arm64-v8a) GOARCH=arm64 ;;
  x86_64)    GOARCH=amd64 ;;
  both)      GOARCH=both   ;;
  *) echo "用法: ./build-apk.sh [arm64-v8a|x86_64|both]"; exit 1 ;;
esac

SDK="${ANDROID_HOME:-/usr/local/android-sdk}"
BT="$SDK/build-tools/36.0.0"
PLATFORM="$SDK/platforms/android-36/android.jar"
[ -d "$BT" ] || { echo "build-tools/36.0.0 缺失"; exit 1; }
[ -f "$PLATFORM" ] || { echo "platforms/android-36 缺失"; exit 1; }

OUT=dist
rm -rf out "$OUT" lib; mkdir -p out/classes "$OUT" lib

build_server() { # $1=abi $2=goarch
  local abi="$1" goarch="$2" goos="android"
  # android/amd64 唯一需 cgo 的组合；x86_64 跑 Linux 容器(waydroid/模拟器)用静态 Linux ELF（同内核 syscall ABI），零 cgo
  [ "$abi" = "x86_64" ] && goos=linux
  echo "== 1/6 交叉编译 Go 服务端 ($abi / $goos/$goarch) =="
  mkdir -p "lib/$abi"
  # 二进制放 lib/<abi>/libmycast.so（nativeLibraryDir）：Android 11+ SELinux 禁止从 files/ exec，
  # nativeLibraryDir 允许 execute（tailscale/syncthing 同法）
  (cd ../go && GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "../android/lib/$abi/libmycast.so" ./cmd/mycast)
  ls -lh "lib/$abi/libmycast.so" | awk '{print "  " $5 "/libmycast.so"}'
}

if [ "$GOARCH" = "both" ]; then
  build_server arm64-v8a arm64
  build_server x86_64 amd64
else
  build_server "$ABI" "$GOARCH"
fi

if [ "$GOARCH" = "both" ]; then
  APK="$OUT/mycast-all.apk"
else
  APK="$OUT/mycast-${ABI}.apk"
fi

echo "== 2/6 aapt2 link (manifest → base.apk) =="
$BT/aapt2 link -o out/base.apk \
  --manifest app/src/main/AndroidManifest.xml \
  -I "$PLATFORM" \
  --auto-add-overlay

echo "== 3/6 javac 编译壳 =="
javac --release 8 -cp "$PLATFORM" -d out/classes \
  app/src/main/java/com/mycast/app/MainActivity.java 2>/dev/null || {
  echo "(javac 警告已屏蔽，重跑显示)";
  javac --release 8 -cp "$PLATFORM" -d out/classes \
    app/src/main/java/com/mycast/app/MainActivity.java;
}

echo "== 4/6 d8 → classes.dex =="
"$BT/d8" --lib "$PLATFORM" --release --min-api 26 --output out/ \
  out/classes/com/mycast/app/*.class
echo "  dex: $(stat -c%s out/classes.dex) B"

echo "== 5/6 dex + lib 并入 apk + zipalign =="
if command -v zip >/dev/null; then
  (cd out && zip -q -j base.apk classes.dex)
  # native 库放 lib/<abi>/libmycast.so（PackageManager 解压到 nativeLibraryDir，SELinux 可执行）
  (cd . && zip -q -r out/base.apk lib)
else
  $BT/aapt add out/base.apk out/classes.dex
  $BT/aapt add out/base.apk lib/arm64-v8a/libmycast.so lib/x86_64/libmycast.so
fi
$BT/zipalign -f 4 out/base.apk "$APK"
rm -rf out/classes

echo "== 6/6 签名（debug key）=="
KS="${DEBUG_KEYSTORE:-$HOME/.android/debug.keystore}"
if [ ! -f "$KS" ]; then
  mkdir -p "$HOME/.android"
  keytool -genkeypair -v -keystore "$KS" -storepass android -alias androiddebugkey \
    -keypass android -keyalg RSA -keysize 2048 -validity 10000 \
    -dname "CN=Android Debug,O=Android,C=US" >/dev/null 2>&1
fi
$BT/apksigner sign --ks "$KS" --ks-pass pass:android --ks-key-alias androiddebugkey \
  --key-pass pass:android "$APK"

echo "== 产物 =="
ls -lh "$APK" | awk '{print "  " $5, $9}'
$BT/apksigner verify --print-certs "$APK" 2>&1 | head -1
echo "OK → $(pwd)/$APK"
