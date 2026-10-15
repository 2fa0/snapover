# 代码审计（2026-10-15）

范围：本次新增的自定义快捷键、历史容量管理、自动剪贴板确认和冲突历史保留，以及它们涉及的存储、队列、粘贴和窗口生命周期。另审计远程桌面保活诊断，独立提交。本次审计不能代替 Windows 实机和跨设备网络验收。

## 修复前的问题清单

| 编号 | 级别 | 位置 | 触发与影响 |
| --- | --- | --- | --- |
| H1 | P1 | `internal/history/store.go:removeFiles`、`capacity.go:SetCapacity` | 文件被占用或目录权限异常时，删除错误被忽略，界面仍显示成功；记录已从索引删除，后续清理不会再次处理残留文件。进程在提交删除记录后退出，也可能遗留同类文件。 |
| H2 | P1 | `internal/history/capacity.go:entryBytesLocked` | 文件大小读取失败被当成零并缓存；刷新占用也复用缓存，可能低估占用，使容量上限失效。 |
| S1 | P1 | `internal/sync/session.go:writeLoop`、`clipboard_result.go:expireClipboard` | 超时检查和发送共用循环，发送阻塞时不能及时检查；超时仅移除确认 ID，尚未开始的排队内容仍可在之后发送。 |
| H3 | P1 | `cmd/snapover/history_storage.go:startHistoryCleanup`、`history_paste.go:completeHistoryPaste` | 后台清理与准备粘贴同时运行，粘贴完成回调依据尚未刷新的列表，可能写回已经进入清理流程的内容。 |
| H4 | P2 | `internal/history/capacity.go:Usage` | 持有历史互斥锁遍历整个目录，慢磁盘或大量文件会阻塞界面上的 List、历史读取和后台入库。 |
| H5 | P2 | `cmd/snapover/main.go` | 应用历史容量上限失败直接退出，历史维护问题会使剪贴板同步、设备连接和文件发送全部不可用。 |
| H6 | P2 | `cmd/snapover/history_storage.go:pollHistoryStorage` | 只在统计成功时校验结果版本；旧统计错误可覆盖更新后的状态，并阻止自动重新统计。 |
| S2 | P2 | `cmd/snapover/window.go:apply`、`internal/sync/engine.go:AttachPeer` | 成功结果只修改最近同步文本，旧失败仍留在侧栏和历史提示；连接切换后，旧连接的完成/失败结果可能覆盖新连接的显示。 |
| K1 | P2 | `internal/keepalive/keepalive.go:report` | 日志仅按跳过原因去重；同一原因下会话、输入桌面或 WTS 查询错误变化会被隐藏，影响定位断开原因。 |

快捷键检查了输入归一化、默认组合保留、新组合注册失败与保存失败回滚；未发现额外的实质问题。

## 修复与验证

- H1：删除错误向上传递；元数据部分成功时仍刷新界面。应用容量、清空及启动维护会重试孤立资源，只删除符合生成规则且不属于现有记录的文件，不递归删除目录。增加删除失败、重启重试和非历史文件保留测试。
- H2：统计错误不再作为零缓存；应用容量重新测量，失败恢复原容量与列表；刷新统计直接读取磁盘。增加文件变化和异常资源回归测试。
- H3：后台清理开始前取消准备中的粘贴，清理完成并刷新列表前拒绝新粘贴请求；增加已经准备完结果但尚未写回的测试。
- H4：锁内只复制记录快照，完整占用统计在锁外进行；列表和入库不再等待整目录扫描。
- H5：启动维护错误记录警告并展示到历史存储设置，应用继续运行；历史索引损坏的加载错误仍按原逻辑处理。
- H6：统计成功、失败结果统一校验版本；过期结果不阻止新统计，增加回归测试。
- S1：确认期限交由独立等待任务处理，无待确认内容时不运行计时器；发送前跳过过期自动队列项，显式 CLI 请求保留自己的期限。增加没有网络发送循环时仍能超时及过期项跳过测试。已经开始的传输不能撤回，超时提示要求检查对端是否写入。
- S2：同步失败与连接状态分开保存；成功清掉此前同步失败提示，保留其他提示。确认携带会话标识，界面忽略旧会话结果；增加成功恢复与旧结果回归测试。
- K1：按原因、会话、协议、桌面、查询错误共同去重，空闲计数变化不重复刷日志；增加会话切换与错误恢复测试。

保活诊断核对了 Microsoft 的 [WTSINFOW](https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/ns-wtsapi32-wtsinfow) 和 [WTS_INFO_CLASS](https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/ne-wtsapi32-wts_info_class) 定义。WTS 查询只用于诊断，失败不阻止原有保活；输入注入成功与会话输入时间更新分别记录，保活仍只在用户开启、已连接、未锁屏的 RDP 会话满足空闲条件时尝试。

验证结果：

- `go test ./...`：通过。
- `go test -race ./internal/sync ./internal/history ./internal/keepalive ./internal/settings`：通过；补充的 CLI 冲突回归也单独通过竞态检查。
- `go vet ./...`：通过。
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/snapover ./cmd/snapover-cli`：通过。
- Windows 下历史、同步、保活及主程序测试代码交叉编译通过（使用 `-exec=/usr/bin/true`，没有执行 Windows 二进制）。
- `SNAPOVER_TEST_UI=1` 的 macOS 原生窗口关闭/重建、后台工作持续、快捷键冲突回滚与历史清理确认测试：通过。
- 各功能拆分提交前使用对应暂存快照运行相关包测试：通过。

Windows RDP 输入注入、会话计时器实际是否更新，以及跨设备弱网体验仍需 Windows 实机验证；本次没有把模拟测试或交叉编译当作实机验收。
