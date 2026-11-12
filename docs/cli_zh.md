# 命令行发送

**简体中文** | [English](cli.md)

`snapover-cli` 复用本机后台 Snapover 的配对和当前连接。新版 `snapover-cli` 需要配套的新版后台；旧版后台会拒绝其请求。请先启动 Snapover 并连接设备，再运行 `snapover-cli`；无需重新配对，也不会打开新的发送窗口。没有在线设备时立即报错，不会排队到下次连接后自动发送。

`build.command` 会把 Windows 的 `snapover-cli.exe` 放在 `Snapover.exe` 旁边，把 Mac 的 `snapover-cli` 放在 `.app/Contents/Resources/` 内，随 ZIP / DMG 一起发布。Windows 的图形程序名已经是 `Snapover.exe`，文件名不区分大小写，所以 CLI 使用独立名称。

## Windows（PowerShell）

可以把解压目录加入 PATH，然后运行 `snapover-cli`，也可以设置短别名（把路径换成实际位置）：

```powershell
Set-Alias snapover "C:\Apps\Snapover\snapover-cli.exe"

snapover send file "C:\Users\xxx\Documents\报告.pdf"
snapover send file "C:\报告.pdf" "C:\截图.png"
snapover send text "你好"
snapover send image "C:\Users\xxx\Pictures\截图.png"
Get-Content -Raw -Encoding UTF8 .\notes.txt | snapover send text --stdin
```

Windows PowerShell 5.1 向原生程序传管道文本时，请先运行 `$OutputEncoding = [System.Text.UTF8Encoding]::new()`；PowerShell 7 默认使用 UTF-8。若要在 PowerShell 5.1 中通过管道读取 CLI 的中文输出，也将 `[Console]::OutputEncoding` 设置为同样的 UTF-8 编码。参见 [PowerShell 编码说明](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_character_encoding)。要让别名在新终端中继续生效，把 `Set-Alias` 那行加入 `$PROFILE`。

## macOS（zsh）

```zsh
alias snapover='/Applications/Snapover.app/Contents/Resources/snapover-cli'

snapover send file './报告.pdf'
snapover send file './报告.pdf' './截图.png'
snapover send text '你好'
snapover send image './截图.jpg'
cat ./notes.txt | snapover send text --stdin
```

把 `alias` 那行加入 `~/.zshrc` 后，新终端也能使用。也可以直接运行 CLI 的完整路径，或把所在目录加入 PATH，使用 `snapover-cli` 命令。

## 行为

文件直接保存到对端接收文件夹，主动发送不设软件大小上限，受接收磁盘空间和文件系统限制。一次可以提供多个文件路径，按给定顺序逐个传输，任一文件失败即停止并报告已完成的文件；每个文件都收到对端保存确认才算成功，不会出现半个批次。文字和图片进入对端剪贴板，不改动发送端剪贴板；成功发送的内容会进入两边复制历史，来源显示为 `Snapover CLI`。

文字上限沿用 Snapover 设置（默认 1 MB，可调至 20 MB），接收端也会校验自己的文本上限；管道输入必须是 UTF-8，保留空行、缩进和末尾换行，跨平台换行按现有规则处理。图片支持 PNG、JPEG、BMP、WebP，原文件和转成 PNG 后都不得超过 20 MB，尺寸沿用现有 4000 万像素上限。要把图片保存为文件而不进入剪贴板，请用 `send file`。

CLI 向执行请求时已连接的设备发送；设备改变或断线时失败，不会改发给另一台设备。文件显示整批进度，可一次提供多个路径；文字和图片收到对端写入剪贴板确认才报告成功。文字和图片遵守本机暂停同步、只同步文本和图片大小设置；对端暂停同步、刚复制过内容或剪贴板写入失败时会报告原因。主动发送文件仍不受剪贴板同步开关影响。 手动同步模式下仍可用 CLI 主动发送文字和图片。

文字和图片的 CLI 发送要求对端也升级到支持接收确认的版本；旧版仍可正常使用普通剪贴板同步。对端刚复制过内容时，内容只存入对端历史，CLI 报告未写入剪贴板并返回非零退出码。不限制总传输时长，也不提供 `--timeout` 选项；持续有实际字节进展就继续，连续 15 分钟没有进展时停止并报告原因（包括等待接收端确认的阶段）。多文件按整批累计字节跟踪，重复的相同进度不会延长等待。超时或按 Ctrl+C 取消时会停止本次传输；必要时关闭共享连接，后台应用会按原有逻辑重连。未收到确认时，先检查对端剪贴板或接收文件夹，再决定是否重试。

## 选项与退出码

选项要放在文件路径或文本之前；文本以 `-` 开头时，在它前面加 `--`：

```sh
snapover send file './大文件.zip'
snapover send text -- '-这是一段文本'
snapover send text --data '/自定义/Snapover数据目录' '你好'
snapover-cli --help
```

CLI 默认读取与桌面应用相同的数据目录。便携版把 `snapover-cli.exe` 留在 `portable.flag` 旁边即可；桌面应用使用了 `-data` 时，CLI 也需指定相同目录。

命令成功返回退出码 `0`，发送或连接失败返回 `1`，参数和输入错误返回 `2`。成功信息写到标准输出，进度和错误写到标准错误，方便脚本处理。

## 从源码编译 CLI

CLI 是控制台程序，不需要 MyGo 窗口环境：

```sh
# macOS
go build -o bin/snapover-cli ./cmd/snapover-cli

# 在 Mac 上交叉编译 Windows x64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/snapover-cli.exe ./cmd/snapover-cli
```
