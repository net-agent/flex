package switcher

import (
	"errors"
	"net"
	"sync"
	"time"
)

var (
	errTooManyConnections = errors.New("too many connections")
	errSourceAddress      = errors.New("source address mismatch")
	errStreamQuota        = errors.New("stream quota exceeded")
	errRateLimited        = errors.New("handshake rate limited")
)

// Limits 是 Switcher 的资源与准入限制。
// 零值表示对应维度不限制；NewServer 会填入一组宽松的默认值，
// 可用 SetLimits 覆盖（必须在 Serve 之前调用）。
type Limits struct {
	// MaxConnections 是同时服务的最大连接数（含握手中）。0 表示不限制。
	MaxConnections int
	// MaxStreamsPerContext 是单节点同时活跃的流数量上限。0 表示不限制。
	MaxStreamsPerContext int32
	// MaxPresenceSubscriptions 是单节点 presence 订阅域名数量上限。0 表示不限制。
	MaxPresenceSubscriptions int
	// HandshakeRatePerIP / HandshakeBurstPerIP 是每 IP 的握手令牌桶参数。
	// Rate 为每秒补充的令牌数，Burst 为桶容量。Rate<=0 表示不做握手限流。
	HandshakeRatePerIP  float64
	HandshakeBurstPerIP int
}

// DefaultLimits 返回默认限制。数值刻意宽松，避免影响正常部署，
// 同时能挡住单机/单 IP 的洪泛式连接与流创建。
func DefaultLimits() Limits {
	return Limits{
		MaxConnections:           4096,
		MaxStreamsPerContext:     8192,
		MaxPresenceSubscriptions: 1024,
		HandshakeRatePerIP:       20,
		HandshakeBurstPerIP:      40,
	}
}

// ipLimiter 是轻量的 per-IP 令牌桶。
// 握手发生在认证之前，是唯一可用的限流维度；桶表设置上限，
// 避免攻击者用大量源 IP 把内存撑大。
type ipLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	rate    float64
	burst   float64
	now     func() time.Time

	lastSweep time.Time
}

type ipBucket struct {
	tokens   float64
	lastSeen time.Time
}

const (
	ipLimiterMaxEntries = 10000
	ipLimiterIdleTTL    = 5 * time.Minute
	ipLimiterSweepEvery = time.Minute
)

func newIPLimiter(rate float64, burst int) *ipLimiter {
	if burst <= 0 {
		burst = 1
	}
	return &ipLimiter{
		buckets:   make(map[string]*ipBucket),
		rate:      rate,
		burst:     float64(burst),
		now:       time.Now,
		lastSweep: time.Now(),
	}
}

func (l *ipLimiter) allow(ip string) bool {
	if l == nil || l.rate <= 0 || ip == "" {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if now.Sub(l.lastSweep) >= ipLimiterSweepEvery {
		l.sweepLocked(now)
	}

	b, ok := l.buckets[ip]
	if !ok {
		if len(l.buckets) >= ipLimiterMaxEntries {
			// 表满：对未知 IP 采取 fail-closed，防止内存无界增长
			return false
		}
		b = &ipBucket{tokens: l.burst, lastSeen: now}
		l.buckets[ip] = b
	}

	if elapsed := now.Sub(b.lastSeen).Seconds(); elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *ipLimiter) sweepLocked(now time.Time) {
	for ip, b := range l.buckets {
		if now.Sub(b.lastSeen) > ipLimiterIdleTTL {
			delete(l.buckets, ip)
		}
	}
	l.lastSweep = now
}

// remoteIP 提取连接的对端 IP。返回空串表示无法识别（如 net.Pipe 等
// 非 IP 地址族），此时跳过按 IP 限流。
func remoteIP(conn net.Conn) string {
	if conn == nil {
		return ""
	}
	addr := conn.RemoteAddr()
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	return host
}
