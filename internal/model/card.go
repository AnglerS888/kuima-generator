package model

type CardKey struct {
	Code       string `json:"code"`
	Amount     int    `json:"amount"`
	UsedBy     int64  `json:"usedBy"`
	UsedByName string `json:"usedByName"`
	UsedAt     string `json:"usedAt"`
	ExpiresAt  string `json:"expiresAt"`
	Note       string `json:"note"`
	CreatedAt  string `json:"createdAt"`
	BatchID    string `json:"batchId"` // 生成批次，用于「导出本次生成」
	Status     string `json:"status"`  // unused | used | expired
}
