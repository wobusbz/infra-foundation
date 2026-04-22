package session

import (
	"math/rand/v2"
	"strconv"
	"sync"
	"time"
)

const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

type tsidState struct {
	mu     sync.Mutex
	lastMs int64
	seq    uint32
}

var globalTsid = &tsidState{}

type SessionID string

func (t SessionID) String() string { return string(t) }

func GenerateSessionID(prefix string) SessionID {
	if len(prefix) > 4 {
		prefix = prefix[:4]
	}

	globalTsid.mu.Lock()
	now := time.Now().UnixMilli()
	if now == globalTsid.lastMs {
		globalTsid.seq++
		if globalTsid.seq > 9999 {
			globalTsid.mu.Unlock()
			time.Sleep(time.Millisecond)
			globalTsid.mu.Lock()
			now = time.Now().UnixMilli()
			globalTsid.lastMs = now
			globalTsid.seq = 0
		}
	} else {
		globalTsid.lastMs = now
		globalTsid.seq = 0
	}
	seq := globalTsid.seq
	globalTsid.mu.Unlock()

	randNum := rand.IntN(62 * 62)

	var buf [20]byte
	pos := 0

	for i := 0; i < len(prefix) && pos < 4; i++ {
		buf[pos] = prefix[i]
		pos++
	}

	tsStr := encodeBase62(now, 8)
	for i := range 8 {
		buf[pos] = tsStr[i]
		pos++
	}

	seqStr := strconv.Itoa(int(seq))
	for i := 0; i < 4-len(seqStr); i++ {
		buf[pos] = '0'
		pos++
	}
	for i := 0; i < len(seqStr); i++ {
		buf[pos] = seqStr[i]
		pos++
	}

	randStr := encodeBase62(int64(randNum), 2)
	for i := range 2 {
		buf[pos] = randStr[i]
		pos++
	}

	return SessionID(string(buf[:pos]))
}

func ParseSessionID(s string) SessionID {
	return SessionID(s)
}

func encodeBase62(num int64, n int) string {
	if num < 0 {
		num = 0
	}
	result := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		result[i] = base62Chars[num%62]
		num /= 62
	}
	return string(result)
}

func decodeBase62(s string) int64 {
	var result int64
	for _, c := range s {
		idx := int64(-1)
		if c >= '0' && c <= '9' {
			idx = int64(c - '0')
		} else if c >= 'A' && c <= 'Z' {
			idx = int64(c - 'A' + 10)
		} else if c >= 'a' && c <= 'z' {
			idx = int64(c - 'a' + 36)
		}
		if idx >= 0 {
			result = result*62 + idx
		}
	}
	return result
}
