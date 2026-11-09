package webassets

import (
	_ "embed"
)

// ============================================================
// 账号相关页面：登录 / 注册 / 我的 / 管理后台
//
// 页面和首页一样 go:embed 进二进制，部署时不用拷 html。
// ============================================================

//go:embed auth.css
var AuthCSS []byte

//go:embed layer.js
var LayerJS []byte

//go:embed login.html
var LoginHTML []byte

//go:embed forgot.html
var ForgotHTML []byte

//go:embed register.html
var RegisterHTML []byte

//go:embed me.html
var MeHTML []byte

//go:embed admin.html
var AdminHTML []byte

// ============================================================
// 404 页面
// ============================================================

//go:embed 404.html
var NotFoundHTML []byte

// ============================================================
// 页面托管
//
// 页面通过 go:embed 打进二进制：部署时不用额外拷贝 html，容器里只有一个可执行文件。
// go:embed 不允许 ".." 越级，所以这个文件必须和页面放在同一个目录（web/）里。
// ============================================================

//go:embed index.html
var IndexHTML []byte

//go:embed bootstrap.min.css
var BootstrapCSS []byte

//go:embed badge.png
var BadgePNG []byte

//go:embed icon.png
var IconPNG []byte
