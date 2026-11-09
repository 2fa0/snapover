package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/egoist/mygo/ui"
)

// Use categories, never raw errors: they can contain names or user paths.
func syncResultCategory(status string) string {
	switch {
	case status == "":
		return "暂无结果"
	case strings.Contains(status, "远程桌面"):
		return "远程桌面剪贴板覆盖了写入结果"
	case strings.Contains(status, "未覆盖剪贴板"):
		return "只存入对端历史，未覆盖剪贴板"
	case strings.Contains(status, "本机跳过"):
		return "本机跳过，未发送"
	case strings.Contains(status, "没发出"), strings.Contains(status, "失败"), strings.Contains(status, "超时"), strings.Contains(status, "未确认"), strings.Contains(status, "断线"), strings.Contains(status, "已断开"):
		return "失败或未确认，请检查对端后重试"
	case strings.Contains(status, "暂停"), strings.Contains(status, "未接收"), strings.Contains(status, "上限"), strings.Contains(status, "未发送"):
		return "被同步策略阻止或对端拒收"
	case strings.Contains(status, "未写入剪贴板"):
		return "未写入剪贴板"
	case strings.Contains(status, "写入") && (strings.Contains(status, "成功") || strings.Contains(status, "已写入") || strings.Contains(status, "已确认")):
		return "对端已确认写入剪贴板"
	default:
		return "尚无可归类的确认结果，请测试连接"
	}
}

func (a *app) buildDiagnosticSummary() string {
	_, connected := a.engine.Link()
	connection := "未连接"
	if connected {
		connection = "已连接（TLS 配对会话）"
	}
	mode := "自动"
	if a.syncMode == 1 {
		mode = "手动"
	}
	result := a.syncStatus
	if a.syncFailure != "" {
		result = a.syncFailure
	}
	return fmt.Sprintf("Snapover\n%s/%s\n%s：%s\n%s：%s\n%s：%t\n%s：%t\n%s：%t\n%s：%s\n%s", runtime.GOOS, runtime.GOARCH, a.tr("连接"), a.tr(connection), a.tr("同步方式"), a.tr(mode), a.tr("隐私模式"), a.privacyMode, a.tr("暂停同步"), a.paused, a.tr("只同步文本"), a.textOnly, a.tr("最近同步结果"), a.tr(syncResultCategory(result)), a.tr("摘要不含剪贴板正文、文件路径、设备名、地址或证书。TCP 测试通过不代表剪贴板写入成功。"))
}

func (a *app) diagnosticSettings(c *ui.Context) {
	if ui.Button(c, a.tr("生成诊断摘要")).Clicked() {
		a.diagnosticSummary = a.buildDiagnosticSummary()
	}
	if a.diagnosticSummary == "" {
		return
	}
	ui.Text(c, a.diagnosticSummary).Selectable().FontSize(12)
	if ui.Button(c, a.tr("复制诊断摘要")).Clicked() {
		a.diagnosticSummary = a.buildDiagnosticSummary()
		item, err := clip.FromText(a.diagnosticSummary)
		if err == nil {
			err = a.board.Write(item)
		}
		if err != nil {
			a.status = "复制诊断摘要失败，请重试"
		} else {
			a.status = "已复制诊断摘要"
		}
	}
}
