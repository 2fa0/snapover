# Snapover 内置升级方案（调研）

状态：Phase 0 与 Phase 1 已实施（2026-10-29），代码、配置与打包流程已写入工作区；发布（Phase 2）和 macOS 签名（Phase 3）待办。GUI 内的实际升级仍需在 Mac / Windows 实机上走一遍。

本文保留当时的方案和验证记录，其中源码保持私有及 `snapover-releases` 地址是旧计划。当前发布地址和源码公开计划以 [README](../README.md#内置升级) 与 `mygo.json` 为准。

已定方向（2026-10-20）：

1. 发布渠道先走 GitHub 公开 releases 仓库，需要时再切到自建 HTTPS（换 `updates.github` 为 `updates.url` 重发一版即可）。
2. macOS 暂时接受 ad-hoc 签名，不阻断功能落地；Developer ID + 公证留到 Phase 3。
3. 自动检查更新默认开启，24 小时一次。

## 1. 结论

1. **不自己写更新器。** MyGo v0.7.3 已经自带完整更新链路（`mygo.Updater`），并且有现成的更新窗口插件（`plugins/updater/native`，纯原生 UI、含简体中文）。它做的事和我们需要的完全一致：检查新版本、显示版本说明、跳过／稍后提醒、下载进度、校验签名、退出后替换应用、重启。
2. **我们只需要三件事**：`mygo.json` 里打开 `updates` 配置、主程序接一个托盘／设置入口、发布时用私钥签名并上传产物。`go.mod`、依赖版本都不用动。
3. **macOS 是唯一的硬前提。** 现在打出来的 `.app` 是 ad-hoc 签名（`Signature=adhoc`，无 Team ID），用它做自更新会出现两个问题：Gatekeeper 在别人的 Mac 上首次打开要手动放行；每次替换二进制后代码标识（cdhash）变化，辅助功能、本地网络这类 TCC 授权预计要重新允许。要顺滑必须上 Developer ID 签名 + 公证（Apple Developer Program，约 ¥688/年）。Windows 不签名也能更新，只是 SmartScreen 提示还在。
4. **发布渠道（已定）**：外置一个只放产物的**公开** releases 仓库（例如 `2fa0/snapover-releases`），源码仓库继续私有；`mygo build -upload` 直接传 GitHub Release。国内网络如果下载慢，后续把 `updates.github` 换成 `updates.url` + R2／对象存储再发一版即可（feed 地址编译进程序，切换需要发一次版本）。

## 2. 接入前基线（保留作方案背景）

| 项目 | 现状 |
| --- | --- |
| 版本号唯一来源 | `mygo.json` 的 `version`（当前 0.8.0），运行时由 MyGo 注入，`appVersionLabel()` 读 `mygo.App.Version()` |
| 打包 | `build.command`：先编两个 CLI，再 `go tool mygo build -platform windows/amd64`、`-platform darwin/arm64`，最后 `ditto` 手工压 Windows ZIP |
| 产物 | `dist/Snapover-0.8.0-windows11-x64.zip`、`dist/darwin-arm64/Snapover.app`、`dist/darwin-arm64/Snapover 0.8.0 arm64.dmg` |
| macOS 签名 | ad-hoc（`codesign -dv` 显示 `flags=adhoc`、`TeamIdentifier=not set`），未公证 |
| Windows 签名 | 无 Authenticode；没打 NSIS 安装包（本机 Homebrew `makensis` 崩溃，脚本主动绕开），只有 ZIP |
| CI | 无 `.github/workflows`；git 也没有 remote |
| 更新相关代码 | 仓库里没有任何自更新实现 |

## 3. MyGo 更新器能提供什么（按 v0.7.3 源码核对）

`mygo.Updater`：

- `Check(ctx)` → 返回更新的 `Version / Notes / Date`，已是最新返回 `nil`；版本按 semver 比较（`1.3.0-beta.2 < 1.3.0-beta.10 < 1.3.0`）。
- `Install(ctx, progress)` → 下载增量或整包，校验后替换应用；**当前进程不受影响**，新版本在 `App.Relaunch()` 后或下次启动生效。
- `Enabled()` → 只有「构建时带 updates 配置」且「安装位置可写」才为真；开发构建、包管理器安装、只读位置（比如直接从 DMG 里运行）返回 false。
- `SetSource(UpdateSource)` → 换 manifest 来源（自带 update 服务时用，仍然强制验签）；`SetPrereleases(true)` → 内测通道。
- 验签：manifest 里的 archive／delta 都带 Ed25519 签名（对文件 SHA-256 签名），公钥编译进程序，私钥签名发布物。**签名不对就不装，应用保持原样。**
- 增量：构建时从已发布的前 3 个版本生成 bsdiff 增量（`updates.deltas` 可调，0 关闭），只发变化文件；增量失败自动回退整包重新下载；落地后逐文件校验大小与 SHA-256，所以结果与官方包完全一致。
- 网络：走系统代理或 `HTTPS_PROXY`；非 loopback 必须是 HTTPS。

平台安装行为：

- **macOS**：`installTarget()` 取 `.app` bundle；`Install` 先在同级临时目录解出新的 bundle，然后起一个 `/bin/sh` 辅助进程（`installAfterExit`）：等主进程退出 → `mv` 旧 bundle 到 work/old → `mv` 新 bundle 就位 → 失败则把旧的搬回来 → 需要重启就用 `/usr/bin/open` 打开。这样 Dock 只有一个图标，不会出现第二份。bundle 里的一切（含 `Contents/Resources/snapover-cli`）都一起换掉。
- **Windows**：`installTarget()` 取 exe 所在目录；`swapFiles` 把更新的顶层条目逐个改名就位（运行中的 exe 允许被改名），旧文件改名成 `.<name>.old-<pid>` 并删除，删不掉的在下次启动由 `cleanUpdateLeftovers` 清理。目标目录里不在更新内的文件（`portable.flag`、`data/`、用户自己放的东西）不会被删。

现成 UI 插件 `plugins/updater/native`（`mygo.Use(native.Plugin)`）：

- 启动后 10 秒查一次，之后按 `Options.Interval`（默认一天）在后台查；
- 有新版本弹窗口：版本说明（Markdown）、**安装更新 / 稍后提醒 / 跳过此版本**；一键下载带进度条，装完询问是否立即重启；
- 可选「以后自动下载并安装」；
- 偏好存在 `PathUserData/updater.json`（`AutomaticChecks`、`AutomaticDownloads`、`LastCheck`、`OnChange`），不碰我们的 `settings.json`；
- 自动跟随系统语言，自带 zh-Hans／zh-Hant 文案；`Options.Strings` 可覆盖文案；
- 菜单入口 `updater.MenuItem()`（「检查更新…」）与手动 `updater.CheckForUpdates()`；
- 应用是 `ActivationPolicyAccessory`（我们就是）时，插件会把窗口显示并抢到前台。

**它不做的事**：降级安装、断点续传、需要管理员权限的写入（`/Applications` 对管理员用户可写，但对标准用户不行）、以及绕过 Gatekeeper／TCC。

## 4. 应用端接入设计

### 4.1 `mygo.json`

```json
{
  "name": "Snapover",
  "slug": "snapover",
  "identifier": "app.snapover.desktop",
  "version": "0.8.0",
  "updates": {
    "publicKey": "…mygo-update.pub 的内容…",
    "github": "2fa0/snapover-releases",
    "changelog": "docs/CHANGELOG.md",
    "deltas": 3
  }
}
```

- `slug` 明确写死：更新包名 `snapover-<version>-<target>.tar.gz`、增量名、DMG 名、安装脚本都以它为准，默认虽然也是 `snapover`，写死避免以后改名踩坑。
- `changelog` 必须指向 `docs/CHANGELOG.md`（默认找仓库根的 `CHANGELOG.md`，我们没有）。取的是 `## 0.8.0` 这一节，现有格式正好匹配，**发布前必须有对应版本小节，否则构建直接失败**。
- `updates.github` 用外置公开仓库，源码仓库可以继续私有；如果那个仓库还放别的东西，加 `tagPrefix`（例如 `desktop-v`）。
- 换成自己的服务时把 `github` 换成 `url`（+ 可选 `s3`）。

### 4.2 签名密钥（一次性）

```sh
go tool mygo keygen          # 生成 mygo-update.key / mygo-update.pub
```

- 公钥进 `mygo.json` 的 `updates.publicKey`（可以提交）。
- 私钥**不进仓库**：构建时用环境变量 `MYGO_UPDATER_PRIVATE_KEY`，或 `updates.privateKey` 指向 `~/` 下的路径。同时存一份到密码管理器——私钥丢了，已装用户就再也收不到更新。
- `.gitignore` 追加 `mygo-update.key`。

### 4.3 主程序接线

`cmd/snapover/main.go`：

```go
import (
    "github.com/egoist/mygo/plugins/updater"
    updaterui "github.com/egoist/mygo/plugins/updater/native"
)

func main() {
    // 放在 mygo.App.SetName 之后、App.Run 之前
    mygo.Use(updaterui.New(updater.Options{Interval: 24 * time.Hour}))
```

`cmd/snapover/tray.go` 的 `trayMenu()`，在「设置」之后插入：

```go
{Label: "检查更新…", Click: func(*mygo.MenuItem, *mygo.Window) { updater.CheckForUpdates() }},
```

`cmd/snapover/window.go` 的 `settingsPage()` 末尾加一组（沿用 `settingsGroup` 写法）：

```go
settingsGroup(c, "关于与更新", func() {
    // 版本行：appVersionLabel()
    // 自动检查开关：初值 updater.AutomaticChecks()，切换调 updater.SetAutomaticChecks(v)
    // 上次检查时间：updater.LastCheck()，用 updater.OnChange 触发重绘
    // 「检查更新」按钮：updater.CheckForUpdates()
})
```

注意点：

- 不要在第一次启动时弹窗打扰。默认自动检查开、静默失败（`Check` 出错就当没查到，只写日志），只有用户点「检查更新…」才把错误显示出来（插件本身就这样）。
- 设置页不要自己再存一份开关，直接用插件的 `updater.json`，避免两处状态不一致。
- 手动「检查更新」会显示被跳过的版本，后台检查不会——保持插件语义即可。
- 关闭窗口只销毁界面，更新窗口由插件独立管理，与 `a.win` 生命周期无关。

### 4.4 更新后的权限与状态

- 数据目录、`peers.json`、历史、`settings.json` 都不在 bundle 内，更新不影响；`updater.json` 同样留在数据目录。
- macOS 的登录项（`SetOpenAtLogin`）指向 bundle 路径，路径不变，不受影响。
- Windows「发送到」快捷方式指向 exe 路径，路径不变，不受影响。
- macOS 服务菜单（`NSServices`）由 Info.plist 声明，新版本 Info.plist 生效后由 LaunchServices 重新登记，无需手动操作。

## 5. 发布流水线

### 5.1 `build.command` 改造

1. 构建前检查 `MYGO_UPDATER_PRIVATE_KEY`（或 `updates.privateKey`），缺失就报错退出——没有私钥打出来的包不带 manifest，已装用户永远升不上来。
2. 构建完成后，把更新产物从 app 目录挪走，**不要塞进 Windows ZIP**：

```sh
mkdir -p dist/releases
mv dist/windows-amd64/*.tar.gz dist/windows-amd64/*.delta \
   dist/windows-amd64/update-*.json dist/windows-amd64/install*.{ps1,sh} dist/releases/ 2>/dev/null || true
mv dist/darwin-arm64/*.tar.gz dist/darwin-arm64/*.delta \
   dist/darwin-arm64/update-*.json dist/darwin-arm64/install*.sh dist/releases/ 2>/dev/null || true
```

3. Windows ZIP 继续用 `ditto` 压 `dist/windows-amd64`（此时只剩 `Snapover.exe`、`snapover-cli.exe`、`resources/`）。
4. 手动上传：在公开仓库创建标签为 `v<version>` 的草稿 Release，上传 `dist/releases/` 的归档、增量和脚本，同时上传 Windows ZIP 与 macOS DMG，**manifest 最后传**（避免清单先到而文件还没到），两个平台齐全后再发布。标签必须与清单 URL 一致，例如版本 `0.8.1` 对应 `v0.8.1`。保留旧版本文件（增量要拿它当基线）。MyGo 的 `build -upload` 会重新构建并上传所选平台的产物，不是上传现有 `dist/releases/`；当前项目应先沿用 `build.command`，避免绕过 CLI 构建和 Windows NSIS 规避步骤。
5. 版本升级顺序：改 `mygo.json` 的 `version` → 在 `docs/CHANGELOG.md` 加 `## x.y.z` 小节 → 构建 → 上传 → 发布 Release。

### 5.2 独立 HTTPS 测试源验收（不用正式发布）

MyGo v0.7.3 的运行时允许 loopback HTTP，但构建配置的 `Updates.validate()` 只接受 HTTPS，因此不能直接用 `updates.url: "http://127.0.0.1:8000"` 打包。使用证书受系统信任的独立 HTTPS 测试地址，并在临时工作副本中进行：

1. 用 `updates.url: "https://<测试域名>/snapover-test"` 替代 `updates.github`，打 0.8.0 安装到独立测试目录；
2. 改版本 0.8.1、同 URL 再打一次，为新版本添加对应 CHANGELOG 小节，产物放 `dist/releases`；
3. 把 0.8.1 的归档及清单放到该 HTTPS 测试地址，确保清单里的 URL 可访问；
4. 打开 0.8.0，点托盘「检查更新…」，走完下载、安装、重启。

测试实例必须使用独立的 `-data` 临时目录，避免动到真实配对与历史。这条路径可验证清单解析、验签、整包安装、bundle 替换及重启；增量回退需额外生成带增量的测试清单后验证。正式构建使用原工作区的 `github` 配置。

## 6. 平台差异与风险

| 风险 | 影响 | 处理 |
| --- | --- | --- |
| macOS 无 Developer ID | 别人机器首次打开仍被 Gatekeeper 拦（现状已如此）；每次更新后辅助功能、本地网络授权预计需重新允许 | 已决定先接受：更新说明里提示「若快捷粘贴失效，去系统设置重新允许辅助功能」；Phase 3 再拿 Developer ID，配 `macos.signingIdentity` + `macos.notarize`（MyGo 会自动 `--options runtime --timestamp` 签名并对 `.app`、`.dmg` staple）。**需实机确认 TCC 是否保留** |
| macOS 从 DMG 里直接运行 | bundle 在只读卷上，`Enabled()` 为 false | 插件窗口会说明原因；README 强调拖进「应用程序」 |
| `/Applications` 不可写（标准用户） | 更新失败并保持旧版本 | 失败即回退（`installAfterExit` 有 `mv` 回滚），提示改用有权限的账号 |
| Windows 装在 `C:\Program Files` | 目录不可写，`Enabled()` false | 文档建议放用户目录；后续可用 CI 打 NSIS 安装包固定装到 `%LOCALAPPDATA%\Programs` |
| Windows 运行中替换 exe | 已被 MyGo 用「改名 + 下次启动清理 `.old-<pid>`」处理 | 验收时确认目录里不残留 `.old-` 文件 |
| Windows 重启时单实例锁 | 新进程可能抢在旧进程释放前启动，导致看起来「退出了但没开」 | MyGo 的 `Relaunch` 在退出流程后启动新进程，理论上安全；**必须实机验证一次**，异常时在 `OnSecondInstance` 里补处理 |
| 国内访问 GitHub Releases 慢／被限 | 更新下载失败或很慢 | 先用 GitHub；不行就换 `updates.url` + R2／对象存储／自有域名，deltas 能显著减小体积 |
| 私钥泄漏 | 任何人都能给你的用户推任意代码 | 私钥只存密码管理器 + CI secret；一旦怀疑泄漏只能换密钥并**重新发布一版带新公钥的版本**（旧版本收不到新更新） |
| 私钥丢失 | 已装用户永久收不到更新 | 多处备份，且把私钥备份当作发版资产 |
| 数据向前兼容 | 新版写出的历史／设置格式，回退旧版可能读不了 | 现有历史已有快照与迁移，但**不支持降级**；README 与 CHANGELOG 说明「升级后不建议回退」 |
| 自动检查的隐私 | 检查会向发布服务器发请求（暴露 IP，无设备标识） | 设置页可关；README 写明请求内容 |
| 断网／纯内网用户 | 检查失败 | 静默失败，不影响任何现有功能；保留手动安装路径 |

## 7. 分阶段实施

已完成：

- **Phase 0**：`go tool mygo keygen -o ~/.config/snapover/update-keys` 生成密钥（公钥已写入 `mygo.json`，私钥路径记录在 `updates.privateKey`，不进仓库）；`mygo.json` 加了 `slug`、`updates`（`github: 2fa0/snapover-releases`、`changelog: docs/CHANGELOG.md`）；`build.command` 加了私钥检查与 `collect_updates`，更新产物集中到 `dist/releases/`，不再混进 Windows ZIP。
- **Phase 1**：`cmd/snapover/updates.go`（插件注册、「检查更新」动作、设置页「关于与更新」组）；`main.go` 注册插件并在 `newApp` 后读取更新状态；托盘菜单加「检查更新…」；`ui_test.go` 的版本断言改为只检查设置页；新增 `updates_test.go`。

已在本机验证：

- `go test ./cmd/snapover`、`go build ./...`、`go vet ./cmd/snapover` 均通过。
- `./build.command` 产出 `dist/releases/` 下的 `snapover-0.8.0-{darwin-arm64,windows-amd64}.tar.gz`、`update-*.json`、`install.sh`；Windows ZIP 里只剩 `Snapover.exe`、`snapover-cli.exe` 和资源文件。
- 用 `mygo.json` 里的公钥对两个归档做了 Ed25519 验签，大小与 manifest 一致；darwin 归档根目录只有一个 `Snapover.app`（`stageBundle` 的前提），内含 `Contents/Resources/snapover-cli`。
- 二进制里确实编进了 feed URL（`https://github.com/2fa0/snapover-releases/releases/latest/download/update-darwin-arm64.json`）与公钥，`Info.plist` 版本为 0.8.0。

待办：

- **Phase 2**：建公开 releases 仓库 `2fa0/snapover-releases`；发版前把 `mygo.json` 版本改为 0.8.1、把 CHANGELOG 的「未发布」改成 `## 0.8.1`；上传 `dist/releases/` 并发布 Release。已发布版本里的第一个带更新器的版本仍需要用户手动安装。
- **实机验收**：本文第 8 节清单，特别是 macOS 替换后权限是否需重授、Windows 重启时单实例锁的时序。
- **Phase 3**：Developer ID + 公证；可选 Authenticode；打包搬 CI。

## 8. 验收清单

macOS：

- [ ] 0.8.0 → 0.8.1 自动检查发现新版本，显示版本说明
- [ ] 增量下载成功；人为改坏增量后能回退整包并装好
- [ ] 装完重启：Dock 只有一个图标，版本号变 0.8.1
- [ ] 历史、配对、设置、`updater.json`、快速历史快捷键全部保留
- [ ] 辅助功能／本地网络权限在更新后是否仍需重新授权（ad-hoc 与 Developer ID 各测一次）
- [ ] 从 DMG 直接运行时点「检查更新…」有明确提示且不破坏现有功能

Windows：

- [ ] 0.8.0 → 0.8.1 更新后 exe 替换成功，重启后是新版本
- [ ] 目录里没有残留 `.<name>.old-<pid>`；`portable.flag`／`data/` 未被删除
- [ ] 「发送到」快捷方式仍可用，单实例行为正常（不会出现两份后台）
- [ ] 放在不可写目录时给出「不能自动更新」的说明

两端通用：

- [ ] 断网、代理错误时静默失败；手动检查显示错误原因
- [ ] 「跳过此版本」后后台不再提示，手动检查仍能看到
- [ ] 设置页开关与 `updater.json` 状态一致，重启后保留

## 9. 已定参数与遗留项

已定：

- 渠道：GitHub 公开 releases 仓库；备选切换路径 `updates.url`。
- 签名：先 ad-hoc（`macos.signingIdentity` 不设），Phase 3 再接 Developer ID。
- 自动检查：默认开，`updater.Options{Interval: 24 * time.Hour}`；不启用 `SetPrereleases`，暂不做内测通道。

遗留项：

- 公开 releases 仓库尚未创建（Phase 2 前置）。
- 私钥已生成并配置到 `updates.privateKey`；首次发布前仍需确认已备份到密码管理器，不因文档更新而重新生成密钥。
- Windows 是否补 NSIS 安装包（决定更新目标目录是用户目录还是 Program Files）留到 Phase 3 一并决定。
