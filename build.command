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
build_tools="$(mktemp -d "${TMPDIR:-/tmp}/snapover-build.XXXXXX")"
trap 'rm -rf "$build_tools"' EXIT
trap 'echo "打包失败，请查看上方错误，处理后重新运行 build.command。" >&2' ERR

ln -s "$go_bin" "$build_tools/go"

echo "正在从 app.jpg 生成程序图标…"
/usr/bin/sips -s format png resources/app.jpg --out resources/icon.png >/dev/null

echo "正在打包 Windows 11 x64…"
# MyGo always invokes NSIS when found. Exclude Homebrew's crashing
# makensis while retaining MyGo's executable icons, manifest and resources.
PATH="$build_tools:/usr/bin:/bin:/usr/sbin:/sbin" CGO_ENABLED=0 \
    "$go_bin" tool mygo build -platform windows/amd64 -o dist

windows_zip="dist/Snapover-${version}-windows11-x64.zip"
/usr/bin/ditto -c -k --keepParent dist/windows-amd64 "$build_tools/windows.zip"
mv "$build_tools/windows.zip" "$windows_zip"

echo "正在打包 Mac M 系列（arm64）…"
CGO_ENABLED=0 "$go_bin" tool mygo build -platform darwin/arm64 -o dist

echo "打包完成："
echo "  Windows 11 x64：$PWD/$windows_zip"
echo "  Mac 应用：$PWD/dist/darwin-arm64/Snapover.app"
echo "  Mac 安装包：$PWD/dist/darwin-arm64/Snapover ${version} arm64.dmg"
