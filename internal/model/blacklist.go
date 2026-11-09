package model

type BlacklistEntry struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
}
