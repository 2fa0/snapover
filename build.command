#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"

if [[ "$(uname -s)" != Darwin ]]; then
    echo "请在 macOS 上运行此脚本，生成 Windows x64 和 Mac M 系列的包。" >&2
    exit 1
fi

go_bin="$(command -v go || true)"
if [[ -z "$go_bin" ]]; then
    echo "未找到 Go，请安装 go.mod 要求的 Go 版本后重试。" >&2
    exit 1
fi
# The Windows build uses a restricted PATH; keep Go reachable by absolute path.
go_bin="$(cd "$(dirname "$go_bin")" && pwd)/$(basename "$go_bin")"
version="$(/usr/bin/plutil -extract version raw -o - mygo.json)"
# Updates are signed, and a build without the key writes no manifests, so
# installed apps could never update to it. Stop instead.
update_key="$(/usr/bin/plutil -extract updates.privateKey raw -o - mygo.json 2>/dev/null || true)"
update_key="${update_key/#\~/$HOME}"
if [[ -z "${MYGO_UPDATER_PRIVATE_KEY:-}" && ( -z "$update_key" || ! -f "$update_key" ) ]]; then
    echo "缺少更新签名私钥（${update_key:-未配置}），打出来的包不会带更新清单，已停止。" >&2
    echo "先生成一次并备份私钥：go tool mygo keygen -o \"$HOME/.config/snapover/update-keys\"" >&2
    exit 1
fi
build_tools="$(mktemp -d "${TMPDIR:-/tmp}/snapover-build.XXXXXX")"
trap 'rm -rf "$build_tools"' EXIT
trap 'echo "打包失败，请查看上方错误，处理后重新运行 build.command。" >&2' ERR

ln -s "$go_bin" "$build_tools/go"

# collect_updates moves the signed update files of a platform out of the
# app's directory, so that the Windows ZIP does not ship them, and keeps
# them together for publishing. Apps update themselves from these files,
# and the app directory should hold only what it runs.
collect_updates() {
    local dir="$1" file
    mkdir -p dist/releases
    for file in "$dir"/*.tar.gz "$dir"/*.delta "$dir"/update-*.json \
        "$dir"/install.sh "$dir"/install.ps1; do
        [[ -e "$file" ]] || continue
        mv "$file" dist/releases/
    done
}

if [[ ! -f resources/app.png ]]; then
    echo "缺少 resources/app.png" >&2
    exit 1
fi

echo "正在编译命令行入口…"
mkdir -p resources/windows-amd64 resources/darwin-arm64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "$go_bin" build -trimpath -ldflags="-s -w" \
    -o resources/windows-amd64/snapover-cli.exe ./cmd/snapover-cli
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 "$go_bin" build -trimpath -ldflags="-s -w" \
    -o resources/darwin-arm64/snapover-cli ./cmd/snapover-cli

echo "正在打包 Windows 11 x64…"
# MyGo always invokes NSIS when found. Exclude Homebrew's crashing
# makensis while retaining MyGo's executable icons, manifest and resources.
PATH="$build_tools:/usr/bin:/bin:/usr/sbin:/sbin" CGO_ENABLED=0 \
    "$go_bin" tool mygo build -platform windows/amd64 -o dist

windows_zip="dist/Snapover-${version}-windows11-x64.zip"
# Before the ZIP, so that it holds the app and nothing else.
collect_updates dist/windows-amd64
/usr/bin/ditto -c -k --keepParent dist/windows-amd64 "$build_tools/windows.zip"
mv "$build_tools/windows.zip" "$windows_zip"

echo "正在打包 Mac M 系列（arm64）…"
CGO_ENABLED=0 "$go_bin" tool mygo build -platform darwin/arm64 -o dist
collect_updates dist/darwin-arm64

echo "打包完成："
echo "  Windows 11 x64：$PWD/$windows_zip"
echo "  Windows CLI：$PWD/dist/windows-amd64/snapover-cli.exe"
echo "  Mac 应用：$PWD/dist/darwin-arm64/Snapover.app"
echo "  Mac CLI：$PWD/dist/darwin-arm64/Snapover.app/Contents/Resources/snapover-cli"
echo "  Mac 安装包：$PWD/dist/darwin-arm64/Snapover ${version} arm64.dmg"
echo "  更新包与清单：$PWD/dist/releases（普通上传时最后传 update-*.json）"
