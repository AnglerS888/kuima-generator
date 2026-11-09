package model

// ---------- 邀请码 ----------
type InviteCode struct {
	Code      string `json:"code"`
	MaxUses   int    `json:"maxUses"`
	Used      int    `json:"used"`
	ExpiresAt string `json:"expiresAt"`
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
	BatchID   string `json:"batchId"` // 生成批次，用于「导出本次生成」
}
