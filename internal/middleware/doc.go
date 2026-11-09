// Package middleware 提供 HTTP 中间件：安全响应头、请求 ID、访问日志、panic 兜底、gzip、CORS、限速与黑名单拦截。
//
// 只依赖 logger/httpx/netutil/store 等下层包，不感知具体业务接口。
package middleware
