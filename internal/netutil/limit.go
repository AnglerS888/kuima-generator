package netutil

import (
	"sync"
	"time"
)

// ipLimiter 是简单的滑动窗口限速器（按客户端 IP）。
type IPLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

var ApiLimiter = NewIPLimiter(RateLimitPerMinute, time.Minute)

var AuthLimiter = NewIPLimiter(authRatePerMinute, time.Minute)

var RegisterLimiter = NewIPLimiter(registerRatePerHour, time.Hour)

var RedeemLimiter = NewIPLimiter(20, time.Minute)

var CaptchaLimiter = NewIPLimiter(60, time.Minute)

var EmailLimiter = NewIPLimiter(6, time.Minute)

func NewIPLimiter(limit int, window time.Duration) *IPLimiter {
	return &IPLimiter{hits: map[string][]time.Time{}, limit: limit, window: window}
}

// allow 记录一次访问，超限返回 false。
func (l *IPLimiter) Allow(ip string) bool {
	now := time.Now()
	cut := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.hits[ip]
	// 丢掉窗口外的记录
	i := 0
	for i < len(list) && list[i].Before(cut) {
		i++
	}
	list = list[i:]
	if len(list) >= l.limit {
		l.hits[ip] = list
		return false
	}
	l.hits[ip] = append(list, now)
	// 顺手清理长期不活跃的条目，防止 map 无限增长
	if len(l.hits) > 4096 {
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}
