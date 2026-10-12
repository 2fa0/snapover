# ClipBridge

局域网同步剪贴板。第一批只做文字和图片，Windows 10/11 与 macOS。

没有托盘，没有自动发现。启动后打开窗口：设置里配对，另外两页看已连接的设备和复制历史。复制之后，另一台机器的剪贴板会变成同一份内容，直接粘贴。

## 运行

两边各启动一次。建议两边都写对方地址，这样断线后任何一边都能重连。

```sh
# Mac
go run ./cmd/clipbridge -peer 192.168.1.20:47821

# Windows
clipbridge.exe -peer 192.168.1.10:47821
```

只写 IP 时端口默认 `47821`。启动日志会打印本机局域网地址，以及证书指纹。第一次连上会信任对方指纹；指纹变了就拒绝，加 `-reset-peers` 再连。

Windows 防火墙要放行入站 TCP 47821。macOS 第一次监听时系统也会问。

看当前剪贴板，不建立连接：

```sh
go run ./cmd/clipbridge -check
```

数据目录：

- macOS：`~/Library/Application Support/ClipBridge`
- Windows：`%AppData%\ClipBridge`

里面有本机证书和 `peers.json`。不要把 `identity/` 拷到另一台机器。

## 这一批做了什么

- 文字，UTF-8，最大 1MB
- 图片，读出来统一按 PNG 传，最大 20MB。再大只跳过并打日志
- 防回环。远端写回剪贴板不会再被同步回去
- 本地 2 秒内刚复制过时，不让对端覆盖剪贴板
- 密码管理器打过标记的内容不同步
- 自签 TLS。首次连接信任指纹，之后钉死

不做：mDNS、配对码、托盘、快捷键、自动粘贴、富文本、文件。

## 编译

依赖钉在 MyGo v0.4.5。本机需要 Go 1.27。

macOS 安装包（Universal `.app` + `.dmg`）：

```sh
go tool mygo build -platform darwin/universal -o dist
```

产物在 `dist/darwin-universal/`。没有 Developer ID 时是 ad-hoc 签名，只能在打包这台 Mac 上直接打开；拿到别的 Mac 会被 Gatekeeper 拦住。

启动后打开窗口，菜单和侧栏是同一组三页：设置（配对）、连接设备、复制历史。`-peer` 会填进设置并开始连接。

```sh
dist/darwin-universal/ClipBridge.app/Contents/MacOS/ClipBridge -peer 192.168.1.20
```

Windows 用同一条命令，产物是没有控制台的窗口程序，界面和 macOS 相同：

```sh
go tool mygo build -platform windows/amd64,windows/arm64 -o dist
```

- `dist/windows-amd64/ClipBridge.exe`
- `dist/windows-arm64/ClipBridge.exe`

这台 Mac 上的 Homebrew `makensis` 一编译脚本就崩溃，所以没有打出 Setup 安装包。把对应的 exe 拷到 Windows 上直接运行即可。

Mac 上编出来的 Windows 程序不能在 Mac 上验证剪贴板。Win+Shift+S 和记事本粘贴必须在 Windows 上试。

## 已知限制

MyGo 的 `WriteImage` 写出 PNG。Windows 会再带一份普通 `CF_DIB`，不是 `CF_DIBV5`；macOS 不会同时写 TIFF。系统截图和普通文本是这一批的完成标准。贴进微信、Word、Photoshop 时透明通道可能丢，先记下来，不挡这一批。
