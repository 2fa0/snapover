# Snapover

**English** | [简体中文](README.zh-CN.md)

Sync your clipboard between Windows and macOS computers on the same **local area network (LAN)**. Copy text, images, files or folders on one computer, then paste them on the other.

## Features

- Sync text, images, files and folders over your local network.
- Find nearby computers automatically, or connect by entering an IP address.
- Pair securely by comparing the six-digit code and confirming on both computers. Connections use TLS and remember trusted devices.
- Browse clipboard history, pin items and use quick paste.
- Send files directly to the other computer's receiving folder.
- Keep syncing from the system tray or menu bar after closing the main window.

## Download and install

Download from the [latest release](https://github.com/2fa0/snapover/releases/latest). Choose the package for your computer:

| System | Package |
| --- | --- |
| Windows 10/11, x64 | `Snapover-*-windows11-x64.zip` |
| macOS 13+, Apple silicon | `snapover-*-arm64.dmg` |
| macOS 13+, Intel | `snapover-*-amd64.dmg` |

**Windows:** Extract the ZIP, keep its files together, and run `Snapover.exe`.

**macOS:** Open the DMG, copy `Snapover.app` to Applications, then launch it from Applications.

## Connect two computers

1. Install and launch Snapover on both computers and connect them to the same local area network.
2. Allow local network access or firewall connections when prompted.
3. Open **Connect devices (连接设备)** and choose **Add device (添加设备)**. Select the other computer; if discovery is unavailable, enter its local IP address.
4. Compare the six-digit pairing code and confirm on both computers.
5. Copy content on one computer and paste it on the other. Keep Snapover running on both computers.

On macOS, quick paste also requires Accessibility permission.

## Updates

Use **Settings → About & updates (设置 → 关于与更新)** or **Check for updates (检查更新)** in the tray/menu bar. Updates are signed and verified before installation. You can also install a newer package from Releases.

## About this repository

This repository hosts Snapover installers, signed update files and user documentation.
