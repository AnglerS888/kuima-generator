// Package imagex 负责图片输入：加载（data URL/URL/base64/本地文件）、安全解码、尺寸防护、logo 与徽标处理。
//
// 依赖 netutil 做 SSRF 防护，不感知业务接口。
package imagex
