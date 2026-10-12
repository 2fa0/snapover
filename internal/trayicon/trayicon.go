// Package trayicon 把托盘图标的源图嵌进二进制里。
//
// 以前 icon.jpg 放在 resources/ 里、运行时从 exe 旁边读：macOS 的
// bundle 里总有它，但 Windows 上只要 exe 单独被拷走（桌面、启动目录、
// 新旧包混放），读不到就会退化成占位竖条。嵌进二进制后任何启动方式
// 都拿得到图。
package trayicon

import _ "embed"

// MenuIconJPG 是托盘/菜单栏图标的源图：白底黑稿，运行时再按主题
// 抠成透明底的黑或白。程序图标是 resources/icon.png（由 app.jpg 转来），
// 不要拿来做菜单图标。
//
//go:embed icon.jpg
var MenuIconJPG []byte
