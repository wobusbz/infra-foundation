package protocol

import "sync"

var bufPools [6]sync.Pool

func init() {
	sizes := []int{256, 512, 1024, 4096, 16384, 65536}
	for i, size := range sizes {
		s := size
		bufPools[i].New = func() any {
			b := make([]byte, s)
			return &b
		}
	}
}

func GetBuf(size int) []byte {
	switch {
	case size <= 256:
		p := bufPools[0].Get().(*[]byte)
		return (*p)[:size]
	case size <= 512:
		p := bufPools[1].Get().(*[]byte)
		return (*p)[:size]
	case size <= 1024:
		p := bufPools[2].Get().(*[]byte)
		return (*p)[:size]
	case size <= 4096:
		p := bufPools[3].Get().(*[]byte)
		return (*p)[:size]
	case size <= 16384:
		p := bufPools[4].Get().(*[]byte)
		return (*p)[:size]
	case size <= 65536:
		p := bufPools[5].Get().(*[]byte)
		return (*p)[:size]
	default:
		return make([]byte, size)
	}
}

func PutBuf(buf []byte) {
	switch cap(buf) {
	case 256:
		bufPools[0].Put(&buf)
	case 512:
		bufPools[1].Put(&buf)
	case 1024:
		bufPools[2].Put(&buf)
	case 4096:
		bufPools[3].Put(&buf)
	case 16384:
		bufPools[4].Put(&buf)
	case 65536:
		bufPools[5].Put(&buf)
	}
}
