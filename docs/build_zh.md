# 构建与发布

**简体中文** | [English](build.md)

面向维护者。日常开发用 `go test ./...` 和 `go run ./cmd/snapover`；源码不公开，README 只面向使用者。

## 环境

- Go 1.27.3 或兼容的更新版本，具体要求以 `go.mod` 为准；不需要 cgo。
- 依赖钉在 MyGo v0.9.0。
- 打包只能在 macOS 上执行。

## 一键打包

```sh
./build.command
```

生成 Windows 11 x64、Mac M 系列（arm64）和 Intel Mac（amd64）的包，Mac 均要求 macOS 13 及以上版本：

- `dist/Snapover-<版本>-windows11-x64.zip`：解压后运行文件夹里的 `Snapover.exe`，保留旁边的资源文件
- `dist/darwin-arm64/Snapover.app` 与 `dist/darwin-arm64/snapover-<版本>-arm64.dmg`
- `dist/darwin-amd64/Snapover.app` 与 `dist/darwin-amd64/snapover-<版本>-amd64.dmg`
- `dist/releases/`：`*.tar.gz`、`*.delta`、`update-*.json`、`install.sh` 等更新产物

文件名中的版本号自动读取 `mygo.json`。脚本的 Windows 步骤会避开 NSIS，生成 ZIP 包，不生成 Setup 安装器；macOS 沿用当前签名配置。

本地测试可运行 `./build-dev.command`：仍生成三个平台的包，但跳过增量更新差集计算，产物放在 `dist/dev/`，不会覆盖正式发布包。签名私钥仍是必需的；正式发布继续使用 `./build.command`。

## 更新签名私钥

`build.command` 会检查更新签名私钥（`mygo.json` 的 `updates.privateKey`，或环境变量 `MYGO_UPDATER_PRIVATE_KEY`），缺失就直接停止——没有私钥打出来的包不带 manifest，已装用户永远升不上来。

官方私钥由维护者保管，不进仓库，也不需要提供给贡献者；首次发布前请确认已备份到密码管理器。私钥丢失后，已安装的用户再也收不到更新；泄漏则相当于交出发布权，只能换新密钥并重新发布一版带新公钥的版本。

生成一次：`go tool mygo keygen -o ~/.config/snapover/update-keys`。

## 单独打某个平台

macOS（Universal `.app` + `.dmg`）：

```sh
go tool mygo build -platform darwin/universal -o dist
```

产物在 `dist/darwin-universal/`。没有 Developer ID 时是 ad-hoc 签名，只能在打包这台 Mac 上直接打开；拿到别的 Mac 会被 Gatekeeper 拦住。

Windows：

```sh
go tool mygo build -platform windows/amd64,windows/arm64 -o dist
```

- `dist/windows-amd64/Snapover.exe`
- `dist/windows-arm64/Snapover.exe`

Windows 用同一条命令，产物是没有控制台的窗口程序，界面和 macOS 相同。这台 Mac 上的 Homebrew `makensis` 一编译脚本就崩溃，所以没有打出 Setup 安装包。把对应的 exe 拷到 Windows 上直接运行即可。

在 Mac 上交叉编译 Windows x64 CLI：

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/snapover-cli.exe ./cmd/snapover-cli
```

## 发布新版本

1. 改 `mygo.json` 的 `version`，并在 `docs/CHANGELOG.md` 加上对应的 `## x.y.z` 小节（更新窗口里的版本说明取自这里，缺了会构建失败）。
2. 运行 `./build.command`。
3. 在 `mygo.json` 的 `updates.github` 指向的仓库创建与版本匹配的草稿 Release，标签为 `v<version>`（例如 `v0.8.1`）。上传 `dist/releases/` 中本版本的归档、增量和安装脚本，以及 `dist/` 下的 Windows ZIP、`dist/darwin-arm64/` 和 `dist/darwin-amd64/` 下的 DMG，最后再传本次生成的 `update-*.json`；目录中可能保留旧版本文件，不要混传。三个目标的文件齐全后再发布。

清单中的下载地址包含这个标签，不能使用其他标签。旧版本的已发布文件要保留：增量更新要从它们生成。

更新地址会编译进程序，更换发布仓库后必须重新打包；已安装的旧包仍使用旧地址，需要手动安装新包一次。

方案与待办见 [内置升级方案](update-plan.md)。

## 资源与图标

图标嵌入代码与图片位于 `internal/resources/`；`resources/` 只放随包发布的文件，避免将源码复制进安装包。

应用图标使用 `resources/app.png`。菜单栏／托盘图标可在设置里选邮筒、小鸟或信封，对应 `internal/resources/icon2.png`、`internal/resources/icon1.png`、`internal/resources/icon3.png`，需保留 PNG 的透明通道；替换后要重新打包并重启程序。

`resources/THIRD_PARTY_NOTICES.txt` 会随整个 `resources/` 目录一起打包，升级依赖或更换第三方素材时应同步更新该清单；随包文件以它和第三方许可证为准，不再附带 `LICENSE`（源码不公开，`mygo.json` 的 `resources` 列表为空）。

## 验证

```sh
# 单个行为或相关包；把包和测试名替换为实际目标
go test ./internal/clip -run TestName -count=1
go test ./internal/sync ./internal/history
go test ./cmd/snapover ./cmd/snapover-cli

# 影响范围较广时
go test ./...
go vet ./...

# 仅格式化本次修改的 Go 文件
gofmt -w path/to/changed.go
git diff --check
```

桌面会话中验证真实窗口的关闭与重建、后台更新、快速历史缩略图加载和系统右键发来的文件请求：

```sh
SNAPOVER_TEST_UI=1 go test ./cmd/snapover -run TestMainWindow -count=1
```

测试使用临时数据目录。

局域网发现的实机测试会使用真实网络，只在需要验证发现功能时运行：

```sh
SNAPOVER_TEST_LAN=1 go test -count=1 ./internal/discovery -run TestLANDiscovery -v
```

macOS 从 Cindy 等桌面应用运行测试时，本地网络权限归属于启动测试的宿主应用；请在系统设置 → 隐私与安全性 → 本地网络中允许该应用，或从 Terminal 中运行。`no route to host` 也可能由本地网络权限拒绝引起，不能仅凭这个错误判断 VPN 路由故障。测试会等待首次授权并重试，超时仍未发现设备时才失败；底层错误会保留在测试输出中。Snapover 本身的发现错误记录在 `logs/snapover.log` 中，重复错误不会持续刷日志。

跨编译只能证明目标平台能够编译，不能替代实机剪贴板、快捷键、权限或窗口行为验证。Mac 上编出来的 Windows 程序不能在 Mac 上验证剪贴板，`Win+Shift+S` 和记事本粘贴必须在 Windows 上试。

### 需要桌面会话的测试

下面这些用例只在设置了环境变量时运行，普通 `go test ./...` 会跳过。改到相关代码时手工跑一遍，默认验证不会覆盖它们。

`SNAPOVER_TEST_UI=1`：

| 测试 | 验证内容 |
| --- | --- |
| `TestCheckModeLeavesRunningAppDataUntouched` | 使用模拟剪贴板启动诊断，不改后台应用的数据文件，也不输出剪贴板正文 |
| `TestMainHistoryPreviewLoadsAndKeepsMainWindow` | 文本和图片的悬停预览、全文与图片查看器替换预览、窗口关闭后不重新打开 |
| `TestMainWindowClosesAndReopensWithoutStoppingBackgroundUpdates` | 关闭并重建主窗口后后台更新继续运行 |
| `TestMainWindowClosedPickerLoadsAndFileRequestsStillWork` | 主窗口关闭时快速历史和文件请求仍可用 |
| `TestMainWindowClosedHistoryMaintenanceStillCompletes` | 主窗口关闭后历史维护仍能完成 |
| `TestPickerTextWindowsLoadAndDismiss` | 快速历史悬停延时后的文本预览、全文替换、分段长文本 |
| `TestNativeQuickHistoryFolderPreviewAndFileNotices` | 快速历史的文件夹预览与本机/远端文件提示 |
| `TestViewerReplacementWaitsForCanceledLoad` | 图片查看器替换时等待已取消的加载 |
| `TestThemeSettingsSwitchAndPersist` | 界面主题切换与持久化 |
| `TestNativeLanguageFollowsSettings` | 界面语言跟随系统设置 |
| `TestShortcutRecorderNativeMenuRestoredAfterCancelAndClose` | 快捷键录制取消或关闭后恢复应用菜单 |
| `TestPickerShortcutNativeChangeKeepsWorkingKeyOnConflict` | 原生快捷键冲突时保留原来可用的键 |
| `TestNetworkGuideWindowReleasesStateAndCanBeRecreated` | 网络引导窗口释放状态并可重建 |
| `TestMacNetworkGuideSettingsLaunchAndCancellation` | macOS：打开本地网络系统设置与取消 |
| `TestMacFileRequestReachesRunningInstanceWithoutOpeningMainWindow` | macOS：Finder 把文件发给正在运行的实例 |
| `TestFinderServiceUsesExistingFileRequestAndRespectsDisable` | macOS：Finder 服务遵守关闭状态 |
| `TestNativeServicePreservesPathsAndDoesNotUseGeneralClipboard` | macOS：Finder 服务传递路径且不占用通用剪贴板 |

`SNAPOVER_TEST_LAN=1` 会针对真实网络运行 `TestLANDiscovery`。本地网络权限被拒绝时该用例会失败，先允许宿主应用或从 Terminal 运行，再判断是否为发现功能缺陷。

## 产物与仓库

`bin/`、`dist/` 和生成的 CLI 二进制是构建产物，不提交。Windows 测试二进制可在 Mac 上编译后拷到 Windows 执行：

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/snapover-clip-windows.test.exe ./internal/clip
```
