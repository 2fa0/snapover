<p align="center">
  <img src="resources/app.png" alt="Snapover" width="96" height="96" />
</p>

<h1 align="center">Snapover</h1>

<p align="center">
  <strong>两台电脑共用一块剪贴板。</strong><br>
  文字、图片、文件和文件夹，复制即到；macOS 与 Windows 原生应用，走局域网，不进云。
</p>

<p align="center">
  <a href="#-亮点">亮点</a> •
  <a href="#-安装">安装</a> •
  <a href="#-首次配对">首次配对</a> •
  <a href="#-常用快捷键">快捷键</a> •
  <a href="#-命令行">命令行</a> •
  <a href="#-从源码构建">构建</a> •
  <a href="#-隐私与安全">隐私与安全</a> •
  <a href="#-许可证">许可证</a>
</p>

<p align="center">
  <a href="https://github.com/2fa0/snapover/releases"><img src="https://img.shields.io/github/v/release/2fa0/snapover?style=flat-square&color=blue" alt="Release" /></a>
  <img src="https://img.shields.io/badge/Go-1.27+-00ADD8.svg?style=flat-square&logo=go" alt="Go" />
  <img src="https://img.shields.io/badge/Platform-macOS%2013%2B%20%7C%20Windows%2010%2F11-brightgreen.svg?style=flat-square" alt="Platform" />
  <img src="https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square" alt="License" />
  <img src="https://img.shields.io/badge/Built%20with-MyGo-6E4AFF.svg?style=flat-square" alt="MyGo" />
</p>

---

## 💡 为什么是 Snapover

把 A 电脑复制的东西，在 B 电脑直接粘贴。不用云盘、不用登录、不进公网。

两台电脑在同一局域网里互相发现，配对后用自签 TLS 1.3 直连。首次配对要核对与本次握手绑定的六位校验码，双方各自确认后才保存证书指纹；之后每次连接都按指纹验证，指纹变了就拒绝。没有账号，没有云端留存，也没有需要维护的服务端。

---

## ⚡ 亮点

- 📋 **复制即同步**：文字默认 1 MB 上限，可在设置里调到 20 MB；换行跨平台对齐（内部 LF，写入 Windows 用 CRLF），空行、缩进和末尾换行都保留，换行格式差异不算新内容。远端写回本地不会再被同步回去。
- 🖼️ **图片**：读出来统一转成 PNG 传输，上限 20 MB、4000 万像素；超限只跳过并写日志，不进历史也不收发。
- 📁 **文件和文件夹**：在 Finder／资源管理器里正常复制，到另一台直接粘贴，不需要打开发送窗口。保留目录结构、空目录，无需手动压缩；剪切也只按复制处理，不删源文件。整批默认上限 20 MB，可调。
- 🔗 **没有中间人**：局域网内直连，同一时间只对一个设备活动；默认不走虚拟网卡，避免把 VPN 地址记成下次要连的地址。
- 🔒 **配对是双向确认**：拒绝、取消或超时都不会自动信任；解除配对立即断开、撤销信任，对端保留旧记录也必须重新获得本机确认。
- ⚡ **快速历史**：`⌥⌘V`（Windows `Alt+V`）弹出搜索式历史，方向键选择、回车或双击直接粘到刚才的光标处；前 9 条置顶自动分配 `⌘1`–`⌘9`。
- 🗂️ **复制历史**：后台全文搜索并高亮命中，支持置顶、备注、图片悬停预览与放大查看、文本全文窗口；未置顶保留 1000 条或 30 天，容量上限默认 2 GB；置顶可导出／导入 ZIP 备份。
- 🎯 **自动或手动同步**：手动模式下普通复制只进本机历史，按「复制并同步」（`⌥⌘C` / `Ctrl+Alt+C`）才发送。
- 🕵️ **隐私可控**：密码管理器标记过的内容不记录、不同步；可暂停、只同步文本、按来源应用排除；隐私模式重启后仍保持。
- 🌐 **跨网络（可选）**：两端连上 Tailscale 后单独开启，局域网地址与 Tailscale 地址分别保存，局域网恢复时不会抢占已建立的隧道连接。
- 📤 **主动发送文件**：Windows 右键「发送到」、macOS Finder「服务」、命令行三个入口，直接存到对端接收文件夹而不动剪贴板；不设软件大小上限，支持暂停、继续和断线续传。
- 💻 **命令行**：`snapover-cli` 复用后台应用已建立的配对与连接，适合脚本和终端工作流。
- 🔄 **内置更新**：发布物用 Ed25519 签名，公钥编译进程序；支持增量下载，增量失败自动改下完整包。
- 🌍 **中英文界面**，可跟随系统；Windows 另有「保持唤醒」开关。
- 🪶 **原生应用**：Go + MyGo 直接画原生控件，不是套壳浏览器，空闲时也不占几百 MB 内存。

细节、边界和实测记录都在 [功能与行为细节](docs/features.md)。

---

## 📦 安装

### macOS（Apple Silicon & Intel）

1. 到 [**Releases**](https://github.com/2fa0/snapover/releases) 下载对应架构的 `.dmg`：
   - **Apple Silicon（M1/M2/M3/M4）**：`snapover-<版本>-arm64.dmg`
   - **Intel x86_64**：`snapover-<版本>-amd64.dmg`
2. 打开磁盘映像，把 **Snapover** 拖进「应用程序」。

> [!NOTE]
> **首次打开**：目前是 ad-hoc 签名、未做 Apple 公证，首次启动请右键（或 Control-点击）`Snapover.app` 选 **打开**，或执行：
> ```sh
> xattr -cr /Applications/Snapover.app
> ```
> 没有 Developer ID 时，包只能在打包那台 Mac 上直接双击打开。

### Windows（10 / 11 x64）

- **ZIP 便携版**：下载 `Snapover-<版本>-windows11-x64.zip`，解压后运行文件夹里的 `Snapover.exe`，同目录的资源文件要一起保留。
- **便携数据**：在 `Snapover.exe` 旁边放一个空文件 `portable.flag`，数据会写到同目录的 `data/`，不写进用户目录。
- Windows 版没有代码签名，SmartScreen 可能提示拦截，选择「更多信息 → 仍要运行」。
- 防火墙要放行入站 TCP `47821`；自动发现还需要 UDP `5353`。尚未配对时，「连接设备」页会直接给出防火墙放行入口。

> [!IMPORTANT]
> **Releases 里的最新版本可能落后于默认分支。** 本 README 描述的是默认分支（`0.10.1`）的行为，其中文件剪贴板、主动发送文件、Tailscale、暂停续传等功能尚未发版。想立刻用上，请按[从源码构建](#-从源码构建)自行编译。

---

## 🚀 首次配对

1. 两台电脑都装好并启动。托盘／菜单栏常驻，关掉窗口不会退出。
2. 打开「连接设备」→「添加设备」，选择发现的电脑。找不到就手填地址，只写 IP 时端口默认 `47821`。
3. 核对两边显示的**六位校验码**相同，然后在两台电脑上各自确认。
4. 连上后会记住设备和地址，下次启动自动连接最近一台；那台拨不通时，同一轮会继续尝试其他已配对设备。

补充：

- 添加窗口开放 2 分钟，单次确认最多等 90 秒；拒绝、取消或超时都不会自动信任，完成确认前不同步剪贴板、也不接收文件。
- macOS 15 及以上首次启动发现时会请求「本地网络」权限，选择允许；macOS 13/14 没有这个入口。辅助功能权限只在第一次使用快捷粘贴时询问。
- 广播被网络隔离、VPN 或系统权限挡住时，可以手填地址；「连接设备」里还能勾选参与发现和搜索的网段。
- 「测试连接」只验证 TCP 端口通不通，不验证 TLS 与配对。

跨网络、多设备、地址优先级等细节见 [功能与行为细节](docs/features.md#tailscale-跨网络连接)。

---

## ⌘ 常用快捷键

| 动作 | macOS | Windows |
| --- | --- | --- |
| 打开快速历史 | `⌥⌘V` 或 `Cmd+Shift+V` | `Alt+V` |
| 复制并同步（手动模式） | `⌥⌘C` | `Ctrl+Alt+C` |
| 粘贴前 9 条置顶 | `⌘1`–`⌘9` | `Ctrl+1`–`Ctrl+9` |
| 只写入剪贴板、不直接粘贴 | `⌥Enter` | `Alt+Enter` |
| 切换当前记录置顶 | `⌘⇧P` | `Ctrl+Shift+P` |
| 删除当前记录 | `⌘⇧⌫` | `Ctrl+Shift+Backspace` |

快速历史里按 Enter 或双击直接粘贴，普通 Backspace 仍编辑搜索词。所有快捷键都能在设置里按键录制、保存或恢复默认；组合被占用时保留原键位。第一次粘贴需要在系统设置 → 隐私与安全性 → 辅助功能里允许 Snapover。

---

## 💻 命令行

`snapover-cli` 复用本机后台已经建立好的配对和当前连接，不需要重新配对，也不打开发送窗口。**先启动 Snapover 并连上设备**，再运行：

```sh
# macOS：为已安装的应用设置别名
alias snapover='/Applications/Snapover.app/Contents/Resources/snapover-cli'

snapover send file './报告.pdf' './截图.png'
snapover send text '你好'
snapover send image './截图.jpg'
cat ./notes.txt | snapover send text --stdin
```

```powershell
# Windows PowerShell：把解压目录加入 PATH，或设别名
Set-Alias snapover "C:\Apps\Snapover\snapover-cli.exe"

snapover send file "C:\Users\me\Documents\报告.pdf"
Get-Content -Raw -Encoding UTF8 .\notes.txt | snapover send text --stdin
```

背下来就够用：文件存到对端接收文件夹，文字和图片进对端剪贴板；成功返回 `0`，发送或连接失败返回 `1`，参数错误返回 `2`；没有在线设备时立即报错，不会排队等下次连接。

完整参数、编码注意事项、退出码和超时规则见 [命令行发送](docs/cli.md)。

---

## 🔄 内置升级

设置 → 关于与更新里可以手动「检查更新」，默认每 24 小时后台检查一次；有新版本时弹出更新窗口，显示版本说明、安装、稍后提醒、跳过此版本。托盘菜单也有「检查更新…」。

发布物用 Ed25519 私钥签名，公钥编译进程序，签名不符或大小不对就不安装，应用保持原样。支持增量更新（从最近 3 个已发布版本生成），增量失败自动改为下载完整包。macOS 退出后替换整个 `.app`，Windows 就地替换并在下次启动清理旧文件，`portable.flag`、`data/` 和同目录其他文件不会被删除。

不能自动更新的情况会说明原因，而不是静默失败：直接从磁盘映像运行的应用、放在不可写目录的程序、以及源码构建（`go run` / `go build`）都需要手动装新版。macOS 目前是 ad-hoc 签名，更新后如果提示没有辅助功能权限，到系统设置里重新允许一次即可。

维护者的打包与发版流程，以及签名私钥的保管要求，见 [构建与发布](docs/build.md)；方案与待办见 [内置升级方案](docs/update-plan.md)。

---

## 🚧 已知限制

- **图片格式**：MyGo 的 `WriteImage` 写出 PNG。Windows 会再带一份 24 位、自下而上的 `CF_DIB`，不是 `CF_DIBV5`；macOS 不会同时写 TIFF。系统截图和普通文本是验收标准，贴进微信、Word、Photoshop、飞书时透明通道可能丢。
- **远程桌面**：通过 RDP 使用 Windows 时，请关闭该连接的剪贴板共享／重定向。`rdpclip.exe` 可能在 Snapover 写入成功后覆盖剪贴板，导致 `Ctrl+V` 无反应而历史回贴正常；检测到这种情况时侧栏会提示关掉剪贴板共享，不会重复写入。已实机确认关闭 RDP 剪贴板同步可以解决。
- **网络切换**：换 Wi-Fi 后会重新绑定物理网卡并更新已配对设备的地址；网络禁用组播时需要在「连接设备」里手填地址。
- **签名**：没有 Developer ID 时 macOS 包只能在打包那台机器上直接打开；Windows 没有代码签名，SmartScreen 可能拦截。
- **不做**：内置跨公网发现或中继、富文本同步。跨公网请自行用 Tailscale 等外部网络，再在设置里允许通过它连接。

---

## 🔧 从源码构建

需要 **Go 1.27.3+**（以 `go.mod` 为准），不需要 cgo。

```sh
git clone https://github.com/2fa0/snapover.git
cd snapover

go test ./...
go run ./cmd/snapover
```

也可以只编译二进制：

```sh
go build -o bin/snapover ./cmd/snapover     # Windows 把输出名改成 bin/Snapover.exe
```

这样得到的是开发版：不包含正式更新配置，不能自动升级；Finder 服务等系统集成功能需要应用包。

一键打包三个平台（仅维护者需要，要求官方更新签名私钥）：

```sh
./build.command
```

产物：`dist/Snapover-<版本>-windows11-x64.zip`、`dist/darwin-arm64/` 与 `dist/darwin-amd64/` 下的 `.app` 和 `.dmg`，以及 `dist/releases/` 里的更新包与清单。本地测试用 `./build-dev.command`，跳过增量差集、产物放 `dist/dev/`。

单独打某个平台：

```sh
go tool mygo build -platform darwin/universal -o dist     # Universal .app + .dmg
go tool mygo build -platform windows/amd64 -o dist        # 交叉编译 Windows x64
```

局域网发现的实机测试会使用真实网络，只在需要验证发现功能时运行：

```sh
SNAPOVER_TEST_LAN=1 go test -count=1 ./internal/discovery -run TestLANDiscovery -v
```

> [!NOTE]
> 跨编译只能证明目标平台能编过，不能替代实机验证。Mac 上编出来的 Windows 程序无法验证剪贴板，`Win+Shift+S` 和记事本粘贴必须在 Windows 上试。macOS 从桌面宿主应用跑测试时，本地网络权限归属于宿主，`no route to host` 不能单独证明路由故障。

打包细节、签名密钥和发版步骤见 [构建与发布](docs/build.md)。

---

## 🔐 隐私与安全

- **数据都在本机**：macOS 在 `~/Library/Application Support/Snapover`，Windows 在 `%AppData%\Snapover`。里面有本机证书、`peers.json`、历史和日志。**不要把 `identity/` 拷到另一台机器。**
- **日志不含正文**：`logs/snapover.log` 是运行日志，`logs/crash.log` 是崩溃记录；两者约 1 MB 轮转、只保留上一份，最早记录超过 7 天的会清理。日志不写剪贴板正文、图片字节或私钥。
- **没有遥测**：不做自动上报，不上传日志。「连接诊断」生成的摘要包含平台、连接状态和结果分类，不含剪贴板正文、路径、设备名、地址或证书。
- **报告安全问题**：请用 GitHub 的[私密漏洞报告](https://github.com/2fa0/snapover/security/advisories/new)，不要开公开 issue；在日志或样例里附上前请先删掉凭据和隐私内容。

历史容量、缓存和崩溃排查的细节见 [功能与行为细节](docs/features.md)。

---

## 🤝 贡献

欢迎 issue 和 pull request。

1. Fork 本仓库，从 `main` 拉分支（`git checkout -b feat/your-feature`）
2. 小改动直接提 PR；涉及协议、持久化、并发或配对安全，请先在 issue 里说清关键约束
3. 提交前跑相关的 `go test`，涉及界面的改动请在 macOS 或 Windows 实机上看一眼
4. 不要提交更新签名私钥、构建产物（`bin/`、`dist/`）或含真实剪贴板内容的日志

代码结构、必须保留的行为约束和验证命令见 [AGENTS.md](AGENTS.md)。

---

## 📄 许可证

Snapover 以 [MIT 许可证](LICENSE)发布，版权归 2fa0 所有。

简单说：可以自由使用、复制、修改、合并、发布、再许可和销售，商用或个人用都行，只要在所有副本里保留版权与许可声明；软件按「现状」提供，不含任何担保。

欢迎个人和公司免费使用、换图标改名后分发，也希望改过的版本能开源回来。作者不鼓励把 Snapover 重新包装成收费产品出售；采用 MIT 后这不再是许可禁止的行为，所以这里只是期望，不是附加条件。

第三方组件与图标保留各自的许可，不因本项目采用 MIT 而改变：图标见 `cmd/snapover/icons/LICENSE*` 与 `cmd/snapover/licenses/`，完整清单见 [THIRD_PARTY_NOTICES.txt](resources/THIRD_PARTY_NOTICES.txt)。`LICENSE` 和 `THIRD_PARTY_NOTICES.txt` 也随安装包分发——macOS 在 `Snapover.app/Contents/Resources/`，Windows 在 exe 同目录。
