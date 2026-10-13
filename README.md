# Snapover

完整的代码现状与功能边界见 [Snapover 项目现状文档 v2](../Snapover项目现状-v2.md)（2026-10-13）。

局域网同步文字和图片剪贴板，也可以主动发送单个文件。支持 Windows 10/11 与 macOS 13 及以上版本。通过局域网自动发现电脑，首次配对需要双方核对校验码并确认，也支持手填地址。

启动后打开窗口。菜单和侧栏是三页：设置、连接设备、复制历史。托盘常驻，关掉窗口不会退出。复制之后，另一台机器的剪贴板会变成同一份内容，直接粘贴。

## 运行

两边各启动一次，在「连接设备」里点击「添加设备」。选择发现的电脑（找不到时手填地址），核对两边相同的六位校验码，然后在两台电脑上确认。添加窗口开放 2 分钟，单次确认最多等待 90 秒；拒绝、取消或超时不会自动信任设备，完成确认前不会同步剪贴板或接收文件。连上后会记住设备和地址。下次启动只自动拨最近一台有地址的已信任设备；拨不通就按 1 秒、2 秒、4 秒重试，最长 30 秒。对方稍后上线时，正在拨号的这边会继续试。两边都在监听，所以只要有一边还能拨到另一边，就能连上。

对端从局域网连进来时，会用它的来源 IP 加上它的监听端口，覆盖已保存的地址。虚拟网卡（Clash、utun、公司 VPN、Tailscale 等）上的连接会丢掉，拨号也绑定到物理网卡，避免把 VPN 地址记成下次要连的地址。设置里仍会列出这些网卡，并标明不用于连接。以前如果已经存了 VPN 地址，改成局域网地址再连。两边保存的地址都失效时，会通过 mDNS / DNS-SD 搜索当前选中的已配对设备；发现的新地址须经过设备身份校验，连接成功后才保存。广播被网络隔离、VPN 或系统权限阻止时，可以手填地址。「连接设备」可以勾选参与广播和搜索的网段，按网卡名和网段记住，不含当前 IP。未设置或点「全选」时使用全部物理网段，之后新出现的也会加入；缩小勾选后，新网段默认不加入。全部取消则停止自动发现，仍可手填地址。

```sh
# Mac
go run ./cmd/snapover -peer 192.168.1.20:47821

# Windows
Snapover.exe -peer 192.168.1.10:47821
```

只写 IP 时端口默认 `47821`。启动日志会打印本机局域网地址，以及证书指纹。首次配对的校验码由本次 TLS 握手的 exporter 派生，两边确认后才保存证书指纹；后续按指纹验证，指纹变了就拒绝。已有配对记录兼容保留。旧版设备只能重连已有配对，首次配对必须两边都升级。解除配对，或加 `-reset-peers`，下次添加需要双方重新确认。

「连接设备」的详情里可以解除单台设备的配对，也可以解除全部配对。解除后立即断开对应连接、停止本机对它的重连并撤销信任。对端即使保留旧记录，再次连接也必须重新获得本机确认，不会自动恢复信任。

Windows 防火墙要放行入站 TCP 47821，自动发现还需要 UDP 5353。macOS 首次访问局域网时需要允许本地网络权限；安装包包含用途说明和 Bonjour 服务声明。发现失败时设备页会显示恢复提示，也可在系统设置 → 隐私与安全性 → 本地网络中检查权限。设置里的「测试连接」只测 TCP 端口通不通，不测 TLS。

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
- 换行跨平台兼容：内部统一 LF，写入 Windows 剪贴板时使用 CRLF，macOS 使用 LF。保留空行、缩进、末尾换行和单独的 CR；换行格式差异不会造成重复同步
- 图片，读出来统一按 PNG 传，最大 20MB。再大只跳过并打日志。设置里可以改图片上限，最大仍是 20MB；超过上限的图片只留在本机历史
- 防回环。远端写回剪贴板不会再被同步回去
- 待发送剪贴板队列最多 8 条且合计不超过 20 MiB；慢连接达到任一限制时会提示队列已满，该次复制仍保留在本机历史。关闭连接后释放待发送内容
- 本地 2 秒内刚复制过时，跳过对端内容，不覆盖剪贴板，也不记录该次远端历史；不会稍后自动重试
- 密码管理器打过标记的内容不同步，也不进历史
- 历史可以搜索、置顶、删除、清空。未置顶的保留 1000 条或 30 天，置顶的不删。长文本正文保存在数据目录的 `texts/` 中，列表只保留短预览；搜索会在后台扫描完整正文。首次启动会自动迁移旧历史，复制完整文本、置顶和清理行为不变
- 历史变更追加到 `history/history.log`，累计 256 次操作或 1 MiB 日志后生成 `index.json` 快照。启动时恢复日志，修复未写完的末尾记录；完整记录校验失败会报错并保留文件
- 历史回写在后台读取和校验图片，避免在界面线程重新计算图片哈希、生成缩略图和重复解码。关闭快速面板或删除待粘贴记录会取消回写
- 列表缩略图由两个后台任务加载，最多排队 32 张；缓存最多 128 张且估算位图内存不超过 8 MiB，离开历史并关闭快速面板一分钟后释放缓存
- 设置里可以暂停同步、只同步文本、改图片上限、开机启动
- macOS 按 `⌥⌘V` 弹出快速历史，方向键或鼠标选择，回车或双击粘贴到刚才的光标处。`Cmd+Shift+V` 也行。第一次粘贴要在系统设置 → 隐私与安全性 → 辅助功能里允许 Snapover。Windows 是 `Alt+V`，同样粘贴到刚才的窗口。没记住刚才的窗口时，只写回剪贴板，需要自己再粘贴一次
- 点托盘图标打开快速历史。右键是状态、快速历史、打开窗口、设置、暂停同步、开机启动、退出
- 快速历史的图片行停留 300 毫秒后显示大图预览，默认在右侧，空间不足时自动换边；鼠标可移入预览，离开后延迟关闭。预览使用已有缩略图按比例显示；点击「查看原图」才加载完整图片，可按原始尺寸滚动查看长截图。同时最多保留一个原图窗口，打开另一张会关闭上一张
- 托盘常驻。关掉窗口不会退出。退出才结束进程
- macOS 开机启动要用打好的 `.app`；源码直接运行不行。Windows 写的是当前 exe 的启动项
- exe 旁边放一个 `portable.flag` 时，数据写到旁边的 `data/`，不写进用户目录
- 自签 TLS 1.3。首次通过与握手绑定的校验码和双方明确确认配对，之后钉住证书指纹；解除配对立即关闭连接
- 剪贴板连续变化会等稳定后再记录和同步。Excel、Figma、IDE 一秒刷很多次时，只保留最后一次
- 历史显示来源应用。macOS 用前台应用名，Windows 用前台进程名。同步到对端后，对端历史也能看到
- 发现和连接默认不走虚拟网卡。诊断里仍会列出，并标明不用于连接。连接设备里可以缩小广播网段；未设置时仍是全部物理网段

不做：跨公网发现或中继、富文本、文件夹和多文件传输。

## 发送文件

两边都更新到支持发送文件的版本并连接后，从应用菜单、托盘右键菜单或侧栏打开「发送文件…」。把一个文件拖入窗口，或点击「选择文件…」，再点击「发送到设备名」。支持最大 50 MB（50 × 1024 × 1024 字节）的普通文件，包括空文件；不支持文件夹和一次选择多个文件。

文件直接保存到对方的接收文件夹，不改变剪贴板，也不会持续同步原文件的后续修改。默认位置是系统的「下载 / Snapover」，可以在设置的「接收文件」里更改。选择目录及开始接收时会检查安全保存能力；文件系统不支持时会提示更换文件夹。已开始接收的文件仍保存到开始时的目录。同名文件自动加序号，不覆盖已有内容。

窗口显示传输进度，收到对方确认保存后才显示成功。关闭发送窗口仍会继续传输。接收端会显示收到文件的通知（需要系统允许通知），可从发送窗口打开接收文件夹或查看最近收到的文件。文本和图片剪贴板同步可以继续使用；主动发送文件不受「暂停同步」和「只同步文本」影响。

传输沿用已信任设备的 TLS 连接，分片写入临时文件，校验完成后发布为正式文件。断线会清理未完成的文件；失败后重新连接并发送，不支持断点续传。发送期间原文件发生修改时会取消本次发送。若确认超时，请先检查对方文件夹，再决定是否重试，以免重复保存。

## Windows 右键「发送到」

Windows 版首次正常启动会在当前用户的「发送到」菜单里添加「Snapover 发送文件」，无需管理员权限。右键一个文件 → 发送到 → Snapover 发送文件，即可在后台直接发送到当前连接的设备，成功时不弹出窗口，不需要拖拽或再次点击发送；发送失败或需要手动处理时才打开发送窗口。Windows 11 如果没有看到「发送到」，先打开「显示更多选项」。

Snapover 已在后台运行时，文件请求会交给原进程，不会再启动一份同步程序。尚未启动时会先启动，并尝试连接最近的已配对设备；10 秒内连上该设备后自动发送。未配对、设备离线或连接到了其他设备时，文件保留在发送窗口，提示连接或确认设备后手动发送，不会无限等待后突然发送。

一次只能选择一个不超过 50 MB 的普通文件；多选、文件夹或已有传输正在进行时会提示，不会覆盖正在发送的文件。可在设置的「接收文件」区域关闭右键入口；重新启用或移动 exe 后再次启动，会更新快捷方式的目标位置。快捷方式直接通过 Windows 系统 Shell Link 接口创建，无需启动 PowerShell；若创建失败，窗口会显示错误，完整原因也会写入 `logs/snapover.log`，仍可使用应用中的发送窗口。

也可以从命令行调用：

```sh
Snapover.exe --send-file "C:\Users\xxx\Documents\报告.pdf"
```

这个 GUI 参数仅在 Windows 启用。跨平台终端发送请使用下面的 CLI。升级后请先退出旧版，再启动新版。

## 命令行发送（PowerShell / zsh）

CLI 复用本机后台 Snapover 的配对和当前连接。请先启动 Snapover 并连接设备，再运行 `snapover-cli`；无需重新配对，也不会打开新的发送窗口。没有在线设备时立即报错，不会排队到下次连接后自动发送。

`build.command` 会把 Windows 的 `snapover-cli.exe` 放在 `Snapover.exe` 旁边，把 Mac 的 `snapover-cli` 放在 `.app/Contents/Resources/` 内，随 ZIP / DMG 一起发布。Windows 的图形程序名已经是 `Snapover.exe`，文件名不区分大小写，所以 CLI 使用独立名称。

PowerShell 中可以把解压目录加入 PATH，然后运行 `snapover-cli`，也可以设置短别名（把路径换成实际位置）：

```powershell
Set-Alias snapover "C:\Apps\Snapover\snapover-cli.exe"

snapover send file "C:\Users\xxx\Documents\报告.pdf"
snapover send text "你好"
snapover send image "C:\Users\xxx\Pictures\截图.png"
Get-Content -Raw -Encoding UTF8 .\notes.txt | snapover send text --stdin
```

Windows PowerShell 5.1 向原生程序传管道文本时，请先运行 `$OutputEncoding = [System.Text.UTF8Encoding]::new()`；PowerShell 7 默认使用 UTF-8。若要在 PowerShell 5.1 中通过管道读取 CLI 的中文输出，也将 `[Console]::OutputEncoding` 设置为同样的 UTF-8 编码。参见 [PowerShell 编码说明](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_character_encoding)。要让别名在新终端中继续生效，把 `Set-Alias` 那行加入 `$PROFILE`。

zsh 中为已安装的 Mac 应用设置别名：

```zsh
alias snapover='/Applications/Snapover.app/Contents/Resources/snapover-cli'

snapover send file './报告.pdf'
snapover send text '你好'
snapover send image './截图.jpg'
cat ./notes.txt | snapover send text --stdin
```

把 `alias` 那行加入 `~/.zshrc` 后，新终端也能使用。也可以直接运行 CLI 的完整路径，或把所在目录加入 PATH，使用 `snapover-cli` 命令。

文件直接保存到对端接收文件夹，沿用单个普通文件最大 50 MB 的限制。文字和图片进入对端剪贴板，不改动发送端剪贴板；成功发送的内容会进入两边复制历史，来源显示为 `Snapover CLI`。文字最大 1 MB，管道输入必须是 UTF-8，保留空行、缩进和末尾换行，跨平台换行按现有规则处理。图片支持 PNG、JPEG、BMP、WebP，原文件和转成 PNG 后都不得超过 20 MB，尺寸沿用现有 4000 万像素上限。要把图片保存为文件而不进入剪贴板，请用 `send file`。

CLI 向执行请求时已连接的设备发送；设备改变或断线时失败，不会改发给另一台设备。文件显示进度，收到对端保存确认才报告成功；文字和图片收到对端写入剪贴板确认才报告成功。文字和图片遵守本机暂停同步、只同步文本和图片大小设置；对端暂停同步、刚复制过内容或剪贴板写入失败时会报告原因。主动发送文件仍不受剪贴板同步开关影响。

文字和图片的 CLI 发送要求对端也升级到支持接收确认的版本；旧版仍可正常使用普通剪贴板同步。默认超时 60 秒，可用 `--timeout` 调整，范围为 `1ms` 至 `10m`。超时或按 Ctrl+C 取消时会停止本次传输；必要时关闭共享连接，后台应用会按原有逻辑重连。未收到确认时，先检查对端剪贴板或接收文件夹，再决定是否重试。

选项要放在文件路径或文本之前；文本以 `-` 开头时，在它前面加 `--`：

```sh
snapover send file --timeout 2m './大文件.zip'
snapover send text -- '-这是一段文本'
snapover send text --data '/自定义/Snapover数据目录' '你好'
snapover-cli --help
```

CLI 默认读取与桌面应用相同的数据目录。便携版把 `snapover-cli.exe` 留在 `portable.flag` 旁边即可；桌面应用使用了 `-data` 时，CLI 也需指定相同目录。命令成功返回退出码 `0`，发送或连接失败返回 `1`，参数和输入错误返回 `2`。成功信息写到标准输出，进度和错误写到标准错误，方便脚本处理。

从源码单独编译 CLI（控制台程序，不需要 MyGo 窗口环境）：

```sh
# macOS
go build -o bin/snapover-cli ./cmd/snapover-cli

# 在 Mac 上交叉编译 Windows x64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/snapover-cli.exe ./cmd/snapover-cli
```

## 编译

依赖钉在 MyGo v0.4.8。本机需要 Go 1.27。

在 Mac 上双击 `build.command`，或在项目目录运行：

```sh
./build.command
```

一键生成 Windows 11 x64 和 Mac M 系列（arm64）的包：

- `dist/Snapover-0.2.0-windows11-x64.zip`：解压后运行文件夹里的 `Snapover.exe`，保留旁边的资源文件
- `dist/darwin-arm64/Snapover.app`
- `dist/darwin-arm64/Snapover 0.2.0 arm64.dmg`：打开后拖入 Applications

文件名中的版本号自动读取 `mygo.json`。脚本的 Windows 步骤会避开 NSIS，生成 ZIP 包，不生成 Setup 安装器；macOS 沿用当前签名配置。

应用图标使用 `resources/app.jpg`，打包脚本会将它转为 `resources/app.png`。菜单栏／托盘图标使用 `resources/icon.png`，请保留 PNG 的透明通道；显示时保持鸟图比例，并按系统主题适配黑色或白色。修改图标后需要重新打包并重启程序。

macOS 安装包（Universal `.app` + `.dmg`）：

```sh
go tool mygo build -platform darwin/universal -o dist
```

产物在 `dist/darwin-universal/`。没有 Developer ID 时是 ad-hoc 签名，只能在打包这台 Mac 上直接打开；拿到别的 Mac 会被 Gatekeeper 拦住。

启动后打开窗口，菜单和侧栏是同一组三页：设置、连接设备（配对）、复制历史。默认打开连接设备页，`-peer` 会填进手填地址并开始连接。

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

局域网发现的实机测试：

```sh
SNAPOVER_TEST_LAN=1 go test -count=1 ./internal/discovery -run TestLANDiscovery -v
```

macOS 从 Cindy 等桌面应用运行测试时，本地网络权限归属于启动测试的宿主应用；请在系统设置 → 隐私与安全性 → 本地网络中允许该应用，或从 Terminal 中运行。`no route to host` 也可能由本地网络权限拒绝引起，不能仅凭这个错误判断 VPN 路由故障。测试会等待首次授权并重试，超时仍未发现设备时才失败；底层错误会保留在测试输出中。Snapover 本身的发现错误记录在 `logs/snapover.log` 中，重复错误不会持续刷日志。

## 已知限制

MyGo 的 `WriteImage` 写出 PNG。Windows 会再带一份普通 `CF_DIB`，不是 `CF_DIBV5`；macOS 不会同时写 TIFF。系统截图和普通文本是这一批的完成标准。贴进微信、Word、Photoshop 时透明通道可能丢。

换 Wi-Fi 后会重新绑定物理网卡，通过发现更新已配对设备的地址；网络禁用组播时，需要在「连接设备」里手填地址。没有 Developer ID 时，macOS 包只能在打包那台 Mac 上直接打开。Windows 没有代码签名，SmartScreen 可能拦截。
