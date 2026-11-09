// Package service 承载可复用的业务逻辑：密码与会话、邮件发信、验证码、自动识别编排、过期清理。
//
// 不直接写 HTTP 响应，错误以 error 返回给接口层。
package service
