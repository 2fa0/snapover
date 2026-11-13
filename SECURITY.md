# Security policy

Snapover carries clipboard contents, images and files between computers, and it
opens a listening port on the local network. Reports about pairing, TLS, the
wire protocol, file handling or anything that could leak clipboard data are
welcome.

## Supported versions

Only the latest release receives fixes. Older versions are not patched, so a
report should be reproducible on the version in
[Releases](https://github.com/2fa0/snapover/releases/latest).

## Reporting a vulnerability

Use GitHub's private reporting: [Report a vulnerability](https://github.com/2fa0/snapover/security/advisories/new).
It stays private between you and the maintainer until an advisory is published.

Please do not open a public issue with the details, and do not attach clipboard
contents, images, private keys or identity files — a description of the format
is enough.

A useful report says: version, platform, what the attacker controls, the steps
that reproduce it, and what it gains. If a fix is straightforward, a patch is
welcome; there is no need to wait for permission to write one.

Expect an acknowledgement within a few days. Fixes ship in the next release and
the advisory credits the reporter unless you prefer otherwise.

## 简体中文

Snapover 会在局域网里转发剪贴板内容、图片和文件，并监听一个端口。与配对、
TLS、网络协议、文件处理相关的漏洞，以及任何可能导致剪贴板内容泄漏的问题，
都欢迎报告。

只有最新版本会得到修复，报告请以
[Releases](https://github.com/2fa0/snapover/releases/latest) 里的当前版本为准。

用 GitHub 的私密报告：[报告漏洞](https://github.com/2fa0/snapover/security/advisories/new)。
在公告发布前，内容只有你和维护者看得到。请不要开公开 issue 描述细节，也不要
附带剪贴板正文、图片、私钥或身份文件——说明格式就够了。

有用的报告包含：版本、平台、攻击者能控制什么、复现步骤，以及由此获得什么。
有补丁更好，不必先问。

一般在几天内回复；修复随下一个版本发布，公告会注明报告者（除非你希望匿名）。
