package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"infra-foundation/config"
	"sync"

	"github.com/cloudwego/netpoll"
)

const (
	HdrLen    = 16
	MaxPktLen = 10 << 20
)

var (
	ErrWrongType   = errors.New("proto: wrong type")
	ErrPktTooLarge = errors.New("proto: packet too large")
	ErrBadMagic    = errors.New("proto: bad magic")
	ErrBadVersion  = errors.New("proto: bad version")
	ErrBadCrc      = errors.New("proto: crc mismatch")
)

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

type Codec struct {
}

func NewCodec() *Codec {
	return &Codec{}
}

func (c *Codec) Pack(t ClusterType, id int32, sid string, payload []byte) ([]byte, error) {
	if t < ClusterHeartbeat || t >= ClusterInvalid {
		return nil, ErrWrongType
	}

	sidBytes := []byte(sid)
	sidLen := len(sidBytes)

	payloadLen := uint32(len(payload))
	total := HdrLen + 2 + sidLen
	total += int(payloadLen)

	buf := GetBuf(total)

	off := 0
	binary.BigEndian.PutUint16(buf[off:off+2], config.Default.ProtocolMagic)
	off += 2
	buf[off] = config.Default.ProtocolVersion
	off += 1
	binary.BigEndian.PutUint32(buf[off:off+4], uint32(total))
	off += 4
	buf[off] = byte(t)
	off += 1
	binary.BigEndian.PutUint32(buf[off:off+4], uint32(id))
	off += 4

	var crcVal uint32
	if config.Default.ProtocolEnableChecksum {
		crcVal = crc32.ChecksumIEEE(payload)
	}
	binary.BigEndian.PutUint32(buf[off:off+4], crcVal)
	off += 4

	binary.BigEndian.PutUint16(buf[off:off+2], uint16(sidLen))
	off += 2
	copy(buf[off:], sidBytes)
	off += sidLen

	copy(buf[off:], payload)
	return buf, nil
}

func (c *Codec) NextPacket(r netpoll.Reader) (netpoll.Reader, error) {
	if r.Len() < HdrLen {
		return nil, nil
	}
	bMagic, err := r.Peek(2)
	if err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(bMagic) != config.Default.ProtocolMagic {
		return nil, ErrBadMagic
	}
	bVer, err := r.Peek(3)
	if err != nil {
		return nil, err
	}
	if bVer[2] != config.Default.ProtocolVersion && bVer[2] != 0x01 {
		return nil, ErrBadVersion
	}
	bLen, err := r.Peek(7)
	if err != nil {
		return nil, err
	}
	pktLen := int(binary.BigEndian.Uint32(bLen[3:7]))
	if pktLen > r.Len() {
		return nil, nil
	}
	if pktLen > MaxPktLen {
		return nil, ErrPktTooLarge
	}
	btyp, err := r.Peek(8)
	if err != nil {
		return nil, err
	}
	t := ClusterType(btyp[7])
	if t < ClusterHeartbeat || t >= ClusterInvalid {
		return nil, ErrWrongType
	}
	return r.Slice(pktLen)
}

func (c *Codec) Unpack(r netpoll.Reader) (*Pkt, error) {
	off := 0
	bMagic, err := r.Next(2)
	if err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(bMagic) != config.Default.ProtocolMagic {
		return nil, ErrBadMagic
	}
	off += 2

	bVer, err := r.Next(1)
	if err != nil {
		return nil, err
	}
	version := bVer[0]
	if version != config.Default.ProtocolVersion && version != 0x01 {
		return nil, ErrBadVersion
	}
	off += 1

	bPkLen, err := r.Next(4)
	if err != nil {
		return nil, err
	}
	pktLen := int(binary.BigEndian.Uint32(bPkLen))
	off += 4

	btyp, err := r.Next(1)
	if err != nil {
		return nil, err
	}
	t := ClusterType(btyp[0])
	off += 1

	bId, err := r.Next(4)
	if err != nil {
		return nil, err
	}
	id := int32(binary.BigEndian.Uint32(bId))
	off += 4

	bCrc, err := r.Next(4)
	if err != nil {
		return nil, err
	}
	expectCrc := binary.BigEndian.Uint32(bCrc)
	off += 4

	var sid string
	if version == config.Default.ProtocolVersion {
		bSidLen, err := r.Next(2)
		if err != nil {
			return nil, err
		}
		sidLen := int(binary.BigEndian.Uint16(bSidLen))
		off += 2

		if sidLen > pktLen-off {
			return nil, fmt.Errorf("invalid sidLen %d > remaining %d", sidLen, pktLen-off)
		}
		if sidLen > 1024 {
			return nil, fmt.Errorf("sidLen %d too large (max 1024)", sidLen)
		}

		if sidLen > 0 {
			bSid, err := r.Next(sidLen)
			if err != nil {
				return nil, err
			}
			sid = string(bSid)
			off += sidLen
		}
	}

	payload, err := r.ReadBinary(pktLen - off)
	if err != nil {
		return nil, err
	}

	if config.Default.ProtocolEnableChecksum && crc32.ChecksumIEEE(payload) != expectCrc {
		_ = r.Release()
		return nil, ErrBadCrc
	}

	_ = r.Release()
	return NewWithSID(t, id, sid, payload), nil
}
