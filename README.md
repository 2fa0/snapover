# Snapover

局域网同步剪贴板。只做文字和图片，Windows 10/11 与 macOS。没有自动发现，要自己填对方地址。

启动后打开窗口。菜单和侧栏是三页：设置、连接设备、复制历史。托盘常驻，关掉窗口不会退出。复制之后，另一台机器的剪贴板会变成同一份内容，直接粘贴。

## 运行

两边各启动一次。第一次在设置里填写对方地址并连接。连上后会记住设备和地址。下次启动只自动拨最近一台有地址的已信任设备；拨不通就按 1 秒、2 秒、4 秒重试，最长 30 秒。对方稍后上线时，正在拨号的这边会继续试。两边都在监听，所以只要有一边还能拨到另一边，就能连上。

对端连进来时，会用它的来源 IP 加上它的监听端口，覆盖已保存的地址。来源地址如果走了 VPN，下次可能拨不回去，要在设置里改成局域网地址。两边保存的地址都失效时，没有自动发现，只能手填。

```sh
# Mac
go run ./cmd/snapover -peer 192.168.1.20:47821

# Windows
Snapover.exe -peer 192.168.1.10:47821
```

只写 IP 时端口默认 `47821`。启动日志会打印本机局域网地址，以及证书指纹。第一次连上会信任对方指纹；指纹变了就拒绝。清除信任，或加 `-reset-peers`，下次连接重新核对。

移除设备会停掉本机正在拨的那个地址，但不会立刻断开已经连上的会话。对端如果还记着地址并再连上来，会再次被信任。要它别再进来，对端也得清掉这边，或者两边都停掉。清除全部信任同样只停掉本机拨号。

Windows 防火墙要放行入站 TCP 47821。macOS 第一次监听时系统也会问。设置里的「测试连接」只测 TCP 端口通不通，不测 TLS。

看当前剪贴板，不建立连接：

```sh
go run ./cmd/snapover -check
```

数据目录：

- macOS：`~/Library/Application Support/Snapover`
- Windows：`%AppData%\Snapover`

里面有本机证书和 `peers.json`。不要把 `identity/` 拷到另一台机器。

日志在同目录的 `logs/`：

- `snapover.log`：普通运行日志
- `crash.log`：崩溃记录。Go panic 和运行时致命错误会把栈写进去。复制后闪退时，崩溃前最后一条通常是正在读的剪贴板格式名，不是内容。下次启动如果上次没正常退出，也会写一条。文件里没有栈时，是原生闪退：macOS 再看 `~/Library/Logs/DiagnosticReports` 里 Snapover 开头的报告，Windows 看事件查看器。

## 这一批做了什么

- 文字，UTF-8，最大 1MB
- 图片，读出来统一按 PNG 传，最大 20MB。再大只跳过并打日志。设置里可以改图片上限，最大仍是 20MB；超过上限的图片只留在本机历史
- 防回环。远端写回剪贴板不会再被同步回去
- 本地 2 秒内刚复制过时，不让对端覆盖剪贴板
- 密码管理器打过标记的内容不同步，也不进历史
- 历史可以搜索、置顶、删除、清空。未置顶的保留 1000 条或 30 天，置顶的不删
- 设置里可以暂停同步、只同步文本、改图片上限、开机启动
- macOS 按 `⌥⌘V` 弹出快速历史，方向键或鼠标选择，回车或双击粘贴到刚才的光标处。`Cmd+Shift+V` 也行。第一次粘贴要在系统设置 → 隐私与安全性 → 辅助功能里允许 Snapover。Windows 是 `Ctrl+Shift+V`，同样粘贴到刚才的窗口。没记住刚才的窗口时，只写回剪贴板，需要自己再粘贴一次
- 点托盘图标打开快速历史。右键是状态、快速历史、打开窗口、设置、暂停同步、开机启动、退出
- 托盘常驻。关掉窗口不会退出。退出才结束进程
- macOS 开机启动要用打好的 `.app`；源码直接运行不行。Windows 写的是当前 exe 的启动项
- exe 旁边放一个 `portable.flag` 时，数据写到旁边的 `data/`，不写进用户目录
- 自签 TLS。首次连接信任指纹，之后钉死

不做：mDNS、6 位配对码、富文本、文件。

## 编译

依赖钉在 MyGo v0.4.5。本机需要 Go 1.27。

在 Mac 上双击 `build.command`，或在项目目录运行：

```sh
./build.command
```

一键生成 Windows 11 x64 和 Mac M 系列（arm64）的包：

- `dist/Snapover-0.1.0-windows11-x64.zip`：解压后运行文件夹里的 `Snapover.exe`，保留旁边的资源文件
- `dist/darwin-arm64/Snapover.app`
- `dist/darwin-arm64/Snapover 0.1.0 arm64.dmg`：打开后拖入 Applications

文件名中的版本号自动读取 `mygo.json`。脚本的 Windows 步骤会避开 NSIS，生成 ZIP 包，不生成 Setup 安装器；macOS 沿用当前签名配置。

macOS 安装包（Universal `.app` + `.dmg`）：

```sh
go tool mygo build -platform darwin/universal -o dist
```

产物在 `dist/darwin-universal/`。没有 Developer ID 时是 ad-hoc 签名，只能在打包这台 Mac 上直接打开；拿到别的 Mac 会被 Gatekeeper 拦住。

启动后打开窗口，菜单和侧栏是同一组三页：设置（配对）、连接设备、复制历史。`-peer` 会填进设置并开始连接。

```sh
dist/darwin-universal/Snapover.app/Contents/MacOS/Snapover -peer 192.168.1.20
```

Windows 用同一条命令，产物是没有控制台的窗口程序，界面和 macOS 相同：

```sh
go tool mygo build -platform windows/amd64,windows/arm64 -o dist
```

- `dist/windows-amd64/Snapover.exe`
- `dist/windows-arm64/Snapover.exe`

这台 Mac 上的 Homebrew `makensis` 一编译脚本就崩溃，所以没有打出 Setup 安装包。把对应的 exe 拷到 Windows 上直接运行即可。

Mac 上编出来的 Windows 程序不能在 Mac 上验证剪贴板。Win+Shift+S 和记事本粘贴必须在 Windows 上试。

## 已知限制

MyGo 的 `WriteImage` 写出 PNG。Windows 会再带一份普通 `CF_DIB`，不是 `CF_DIBV5`；macOS 不会同时写 TIFF。系统截图和普通文本是这一批的完成标准。贴进微信、Word、Photoshop 时透明通道可能丢。

换 Wi-Fi 后如果两边保存的地址都失效，要在设置里重填。没有 Developer ID 时，macOS 包只能在打包那台 Mac 上直接打开。Windows 没有代码签名，SmartScreen 可能拦截。
