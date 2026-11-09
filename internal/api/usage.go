package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"wxcode/internal/httpx"
	"wxcode/internal/netutil"
	"wxcode/internal/qrcode"
	"wxcode/internal/store"
)

// ============================================================
// 使用策略与记录（改规则只改这里）
//
//	调用方式                  生成 / 解码 / 好友码
//	API Key 调用              账号必须有效；每次成功调用扣 1 次额度
//	网页 + require_login=开   必须登录；每次成功调用扣 1 次额度
//	网页 + require_login=关   匿名可用、不扣额度（但**照记**，用户显示"未知用户"）
//
// 扣减在进业务前完成，失败会 refundQuota 退回；记录只写"成功"的那一次。
// ============================================================

// demoPayloads 是"示例内容"：用它生成/解码既不计费也不留记录，
// 免得首页默认值把免费额度刷掉、把记录表刷满。
var demoPayloads = map[string]bool{
	"https://example.com":      true,
	"https://example.com/":     true,
	"https://www.example.com":  true,
	"https://www.example.com/": true,
}

// isDemoPayload 判断是不是示例内容（忽略首尾空白与末尾斜杠）。
func isDemoPayload(payload string) bool {
	s := strings.TrimSpace(payload)
	if demoPayloads[s] {
		return true
	}
	// 大小写不敏感地再兜一次
	return demoPayloads[strings.ToLower(s)]
}

// gateUsage 三个网页功能共用的准入检查；ok=false 表示已经写好响应，调用方直接返回。
//
// meter=false 表示这次"不需要计费"（示例内容 / 重复内容），但**登录要求照样生效**。
func gateUsage(w http.ResponseWriter, r *http.Request, meter bool) (ai *authInfo, consumed bool, ok bool) {
	ai = authOf(r)

	// 1) 匿名：要求登录就拦下，前端据此跳登录页
	if ai.user == nil {
		if ai.viaAPI {
			httpx.WriteErr(w, http.StatusUnauthorized, errors.New("API Key 无效"))
			return ai, false, false
		}
		if store.LoadSettings().RequireLogin {
			// 没带 Key 也没登录的调用（网页和脚本都可能是这个形态），把两条路都写清楚
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error":     "该功能需要登录后使用；接口调用请携带 X-API-Key（登录后在「我的」页面复制）",
				"needLogin": true,
			})
			return ai, false, false
		}
		return ai, false, true // 匿名可用；不计费
	}

	store.TouchUserIP(ai.user.ID, netutil.ClientIP(r))

	// 2) 该不该扣：API Key 一定扣；网页要登录限制开着才扣；不计费的直接放过
	shouldCharge := meter && (ai.viaAPI || store.LoadSettings().RequireLogin)
	if !shouldCharge {
		return ai, false, true
	}
	if !store.ConsumeQuota(ai.user.ID) {
		httpx.WriteErr(w, http.StatusForbidden, errQuotaNoLeft)
		return ai, false, false
	}
	return ai, true, true
}

// refundUsage 把 gateUsage 扣掉的次数退回。
func refundUsage(ai *authInfo) {
	if ai == nil || ai.user == nil {
		return
	}
	store.RefundQuota(ai.user.ID)
}

// recordGeneration 记一条生成记录。
//
// 未登录也写：user_id = 0、username 为空，后台显示「未知用户」——
// 需求就是"谁都能用的时候也得留痕（IP / 时间 / 内容）"。
func recordGeneration(r *http.Request, ai *authInfo, req *encodeReq, info *qrcode.EncodeInfo, size int, cost time.Duration) {
	if info == nil {
		return
	}
	// 示例内容、重复内容：不写记录（省得把记录表刷成一样的行）
	if isDemoPayload(info.Payload) {
		return
	}
	storedPayload, enabled := store.RecordStorePayload(info.Payload)
	if !enabled {
		return
	}
	if ai.user != nil && store.HasDone(ai.user.ID, "gen", info.Payload) {
		return
	}
	source := "web"
	if ai.viaAPI {
		source = "api"
	}
	detail := map[string]any{
		"version":  info.Version,
		"level":    string(qrcode.LevelNames[info.Level]),
		"mask":     info.Mask,
		"typebits": info.Typebits,
		"mode":     qrcode.ModeName(info.Mode),
		"size":     req.Size,
		"format":   req.Format,
	}
	if req.Logo != "" {
		detail["logo"] = true
	}
	rec := store.NewRecord(ai.user, "gen", source, storedPayload, req.Format, detail,
		size, int(cost.Milliseconds()), netutil.ClientIP(r), r.UserAgent())
	_ = store.InsertRecord(rec)
}

// recordDecode 记一条解码记录。
func recordDecode(r *http.Request, ai *authInfo, res *qrcode.DecodeResult, cost time.Duration) {
	if res == nil {
		return
	}
	if isDemoPayload(res.Payload) {
		return
	}
	storedPayload, enabled := store.RecordStorePayload(res.Payload)
	if !enabled {
		return
	}
	if ai.user != nil && store.HasDone(ai.user.ID, "decode", res.Payload) {
		return
	}
	source := "web"
	if ai.viaAPI {
		source = "api"
	}
	detail := map[string]any{
		"family":      res.Family,
		"version":     res.Version,
		"ecLevel":     res.ECLevel,
		"mask":        res.Mask,
		"mode":        res.Mode,
		"corrections": res.Corrections,
	}
	rec := store.NewRecord(ai.user, "decode", source, storedPayload, "", detail,
		0, int(cost.Milliseconds()), netutil.ClientIP(r), r.UserAgent())
	_ = store.InsertRecord(rec)
}

// recordQRDecode 好友码（普通二维码）解析，也按 decode 记一条。
func recordQRDecode(r *http.Request, ai *authInfo, res *qrcode.QRResult, cost time.Duration) {
	if res == nil {
		return
	}
	source := "web"
	if ai.viaAPI {
		source = "api"
	}
	payload := res.URL
	if payload == "" {
		payload = res.Text
	}
	if isDemoPayload(payload) {
		return
	}
	storedPayload, enabled := store.RecordStorePayload(payload)
	if !enabled {
		return
	}
	if ai.user != nil && store.HasDone(ai.user.ID, "decode", payload) {
		return
	}
	detail := map[string]any{
		"family":      "QR",
		"version":     res.Version,
		"ecLevel":     res.ECLevel,
		"mask":        res.Mask,
		"mode":        res.Mode,
		"modules":     res.Modules,
		"corrections": res.Corrections,
	}
	rec := store.NewRecord(ai.user, "decode", source, storedPayload, "", detail,
		0, int(cost.Milliseconds()), netutil.ClientIP(r), r.UserAgent())
	_ = store.InsertRecord(rec)
}
