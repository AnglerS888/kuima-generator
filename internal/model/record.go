package model

// ============================================================
// 生成 / 解析记录
//
// 一条记录 = 一次成功的生成或解析。payload 存"内容本身"（生成的文本 / 解出的文本），
// detail 存结构化附加信息（版本、等级、掩码…）的 JSON，方便以后按字段查询而不用改表。
// ============================================================

type Record struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	Kind      string `json:"kind"`   // gen | decode
	Source    string `json:"source"` // web | api
	Payload   string `json:"payload"`
	Detail    string `json:"detail"`
	Format    string `json:"format"`
	SizeBytes int    `json:"sizeBytes"`
	MS        int    `json:"ms"`
	IP        string `json:"ip"`
	UA        string `json:"ua"`
	CreatedAt string `json:"createdAt"`
}
