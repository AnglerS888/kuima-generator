<div align="center">

<img src="docs/logo.png" width="128" alt="葵花码工坊">

# 葵花码工坊 · Android

在手机上**离线**生成带 Logo 的微信葵花码，也能解析葵花码图片。

[![Release](https://img.shields.io/github/v/release/Taocrypt/wxcode-studio?color=07C160)](https://github.com/Taocrypt/wxcode-studio/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Taocrypt/wxcode-studio/total?color=07C160)](https://github.com/Taocrypt/wxcode-studio/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
![Platform](https://img.shields.io/badge/Android-8.0%2B%20arm64-3DDC84)

[下载](https://github.com/Taocrypt/wxcode-studio/releases/latest) · [功能](#功能) · [使用说明](#使用说明) · [常见问题](#常见问题) · [从源码构建](#从源码构建) · [更新日志](CHANGELOG.md)

<img src="docs/example.png" width="260" alt="示例葵花码">

<sub>示例：用本项目生成、内容为本仓库地址的葵花码，可用微信扫一扫试试</sub>

</div>

本项目是 [SunXin121/wxcode-studio](https://github.com/SunXin121/wxcode-studio) 的 Android 移植版。上游是一个 Web 服务，在服务器上用 unidbg 模拟运行微信的原生库来生成葵花码；这里改为在 arm64 手机上直接加载这些原生库，不需要服务器。

## 功能

- **生成葵花码**：输入网址或任意文字，可选一张图片作为中间 Logo，生成 1200×1200 PNG，保存到相册或直接分享。
- **原文模式**：输入不是网址时按原文编码（中文、空格、换行均可，最多 512 字节），微信扫码直接显示文字。
- **回读校验**：生成后用本机的原生解码器把码读一遍，确认内容与输入完全一致。
- **解析葵花码**：从相册选一张葵花码或二维码图片，读出内容，可复制、打开或拿去重新生成。
- **小程序转链（可选）**：微信扫码时会把 `#小程序://` 当成普通文字。填入你自己的 [C1N](https://www.c1n.cn) token、小程序 appId 和页面路径，App 会调用 C1N 开放接口换成 https 短链再生成葵花码，扫码即可打开小程序。
- 支持从其他 App「分享」文字到本 App，自动提取其中的链接。

除「小程序转链」外，所有操作都在本机完成，不上传任何内容。

## 下载安装

到 [Releases](https://github.com/Taocrypt/wxcode-studio/releases/latest) 下载 `wxcode-studio-<版本>.apk` 安装。

- 系统要求：Android 8.0（API 26）及以上，**仅支持 64 位 ARM（arm64-v8a）**。
- 各版本使用同一签名，可直接覆盖升级。

## 使用说明

1. 在「链接或文字」里输入网址（如 `https://example.com`，也可省略 `https://`）或任意文字。
2. 可选：点「选择图片」设置中间 Logo，不选则中间留白。
3. 点「生成葵花码」，下方显示结果和校验状态，可保存或分享。
4. 生成小程序码：在「小程序转链（C1N）」里填 token（C1N 控制台 → 个人中心 → 短链配置）、appId、页面路径，点「转成短链并生成葵花码」。不知道 appId 时，可把 `#小程序://` 链接发给公众号「C1N短链接服务」查询。
5. 解析：在「解析葵花码」里点「选择图片解析」。

> 建议正式使用前用微信实际扫一次确认。

## 和上游 Web 版的区别

| | 上游 wxcode-studio | 本项目 |
|---|---|---|
| 运行方式 | 服务器 + unidbg 模拟 | 手机直接加载原生库 |
| 是否联网 | 需要访问服务 | 默认完全离线 |
| 内容类型 | http(s) 网址 | 网址、任意文字、小程序（经 C1N 转链） |
| 解析葵花码 | 无 | 支持 |
| 平台 | 任意浏览器 | Android 8.0+ arm64 |

## 隐私

- 生成、校验、解析都在本机完成，App 不收集任何数据，没有统计和广告。
- 只有使用「小程序转链」时，会把你填写的 token、appId、页面路径和密钥发送到 C1N（`https://c1n.cn`）。token、appId、页面路径保存在 App 私有存储中，方便下次使用；密钥不保存。
- 网络权限仅用于这一功能。

## 常见问题

**微信扫码只显示一串文字，没有跳转？**
微信只会对 http(s) 网址做跳转。`#小程序://` 链接和普通文字都会被当成文本显示。要扫码打开小程序，请用「小程序转链」换成 https 短链再生成。

**提示「只支持 64 位 ARM 手机」或闪退？**
内置的微信原生库只有 arm64-v8a 版本，32 位手机和 x86 模拟器无法运行。

**内容长度有限制吗？**
最多 512 字节：网址按 UTF-8 计，中文约 170 个字。

**生成的码和微信官方小程序码一样吗？**
外观由同一套原生库生成，样式一致。但普通网址生成的葵花码并非微信后台签发的小程序码，扫码后按网址或文字处理。

**升级会丢数据吗？**
所有版本使用同一签名，直接覆盖安装即可，已填写的 token 等设置会保留。

## 已知限制

- 只支持 arm64-v8a。
- 「小程序转链」依赖第三方服务 C1N，需要自己注册并获取 token；`#小程序://` 链接能否直接转换取决于 C1N 接口，失败时请改用 appId + 页面路径。
- 原生库来自微信，后续微信版本的兼容性不作保证，正式使用前请实扫确认。

## 从源码构建

本项目不依赖 Gradle，用一个 shell 脚本直接调用 Android 构建工具。

需要：JDK 11+、`aapt2`、`zipalign`、`d8.jar`、`apksigner.jar`、Android 34 的 `android.jar`。

```bash
# 指定工具路径（按你的环境修改）
export JDK=/path/to/jdk/bin
export AAPT2=$ANDROID_HOME/build-tools/34.0.0/aapt2
export ZIPALIGN=$ANDROID_HOME/build-tools/34.0.0/zipalign
export BT=$ANDROID_HOME/build-tools/34.0.0      # 需包含 lib/d8.jar 和 lib/apksigner.jar
export AJ=$ANDROID_HOME/platforms/android-34/android.jar

# 可选：指定签名，不指定则自动生成一个调试密钥
export KS=/path/to/release.jks KS_PASS=xxx KS_ALIAS=xxx

VC=5 VN=1.4 bash app/build.sh    # 输出 app/build/app.apk
```

## 项目结构

```
app/
  AndroidManifest.xml
  build.sh                       构建脚本
  src/com/wxcode/studio/
    MainActivity.java            界面（纯代码布局，无 XML）
    WxEngine.java                调用原生库编码、渲染 SVG、回读解码
    UrlNormalizer.java           网址规范化（与上游 validation.py 规则一致）
    C1nClient.java               C1N 开放接口（小程序短链 / 普通短链）
  src/com/tencent/qbar/          原生库的 JNI 接口声明
  jni/arm64-v8a/                 原生库（libwechatQrMod.so 已打补丁）
  assets/                        角标图片与扫码模型
tools/
  unidbg-bridge/                 用 unidbg 在电脑上调用原生库的测试桥（编码 / 解码）
  render_svg.py                  把编码输出的 SVG 渲染成 PNG（PIL）
  make_logo.py                   生成项目 Logo（PIL）
  disas.py                       反汇编辅助脚本
docs/
  technical-notes.md             技术说明
  logo.png / example.png         README 用图
```

## 原理简述

- `libwechatQrMod.so` 的 `Encode`（format 18）原本按内部标志决定是否输出 SVG。在偏移 `0x78d40` 处把 `ldrb w8,[x20,#0x90]`（`88424239`）改为 `mov w8,#1`（`28008052`），使其始终把 SVG 写入输出缓冲区。
- SVG 中的 image / circle / line 元素用 Android Canvas 绘制；中间 Logo 裁成半径 130 的圆，位于 (300,300)。
- 回读校验使用 `QbarNative.Init / ScanImage / GetOneResult`，模型首次运行时从 assets 复制到应用私有目录。

## 致谢

- [SunXin121/wxcode-studio](https://github.com/SunXin121/wxcode-studio)：上游 Web 版，提供了原生库调用方式与渲染规则。
- [unidbg](https://github.com/zhkl0228/unidbg)：离线测试时用于模拟运行 arm64 原生库。
- [C1N 短链接](https://www.c1n.cn)：小程序短链开放接口。

## 协议

本仓库自有代码采用 [MIT](LICENSE) 协议。原生库与模型的版权归腾讯所有，不在 MIT 授权范围内，详见 [NOTICE.md](NOTICE.md)。

本项目与腾讯、微信无任何关联，仅供学习研究。
