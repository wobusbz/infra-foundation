package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"infra-foundation/config"
	"sync"

	"github.com/cloudwego/netpoll"
)

const (
	HeadLength    = 16
	MaxPacketSize = 10 << 20
)

var (
	ErrWrongPacketType  = errors.New("codec: wrong packet type")
	ErrPacketSizeExcced = errors.New("codec: packet size exceed")
	ErrWrongMagic       = errors.New("codec: wrong magic")
	ErrWrongVersion     = errors.New("codec: wrong version")
	ErrChecksumMismatch = errors.New("codec: checksum mismatch")
)

var packPools [6]sync.Pool

func init() {
	sizes := []int{256, 512, 1024, 4096, 16384, 65536}
	for i, size := range sizes {
		s := size
		packPools[i].New = func() any {
			b := make([]byte, s)
			return &b
		}
	}
}

func getPackBuffer(size int) []byte {
	switch {
	case size <= 256:
		p := packPools[0].Get().(*[]byte)
		return (*p)[:size]
	case size <= 512:
		p := packPools[1].Get().(*[]byte)
		return (*p)[:size]
	case size <= 1024:
		p := packPools[2].Get().(*[]byte)
		return (*p)[:size]
	case size <= 4096:
		p := packPools[3].Get().(*[]byte)
		return (*p)[:size]
	case size <= 16384:
		p := packPools[4].Get().(*[]byte)
		return (*p)[:size]
	case size <= 65536:
		p := packPools[5].Get().(*[]byte)
		return (*p)[:size]
	default:
		return make([]byte, size)
	}
}

// TryPutPackBuffer 尝试将打包缓冲区放回 pool；如果容量不属于 pool 分桶则直接丢弃。
func TryPutPackBuffer(buf []byte) {
	c := cap(buf)
	switch c {
	case 256, 512, 1024, 4096, 16384, 65536:
		for i, size := range []int{256, 512, 1024, 4096, 16384, 65536} {
			if c == size {
				packPools[i].Put(&buf)
				return
			}
		}
	}
}

type Codec struct {
	buf  *bytes.Buffer
	size int32
	Id   int32
	sid  int64
	typ  Type
}

func NewCodec() *Codec {
	return &Codec{buf: bytes.NewBuffer(nil), size: -1}
}

func (p *Codec) Pack(typ Type, id int32, sid int64, payload []byte) ([]byte, error) {
	if typ < Heartbeat || typ >= Invalid {
		return nil, ErrWrongPacketType
	}
	payloadLen := uint32(len(payload))
	total := HeadLength
	if p.isSidOffset(typ) {
		total += 8
	}
	total += int(payloadLen)

	buf := getPackBuffer(total)

	offset := 0
	binary.BigEndian.PutUint16(buf[offset:offset+2], config.Default.ProtocolMagic)
	offset += 2
	buf[offset] = config.Default.ProtocolVersion
	offset += 1
	binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(total))
	offset += 4
	buf[offset] = byte(typ)
	offset += 1
	binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(id))
	offset += 4

	var crcVal uint32
	if config.Default.ProtocolEnableChecksum {
		crcVal = crc32.ChecksumIEEE(payload)
	}
	binary.BigEndian.PutUint32(buf[offset:offset+4], crcVal)
	offset += 4

	if p.isSidOffset(typ) {
		binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(sid))
		offset += 8
	}

	copy(buf[offset:], payload)
	return buf, nil
}

func (p *Codec) isSidOffset(typ Type) bool {
	return typ == ClientData || typ == InternalData
}

func (p *Codec) NextPacket(reader netpoll.Reader) (netpoll.Reader, error) {
	if reader.Len() < HeadLength {
		return nil, nil
	}
	bMagic, err := reader.Peek(2)
	if err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(bMagic) != config.Default.ProtocolMagic {
		return nil, ErrWrongMagic
	}
	bVer, err := reader.Peek(3)
	if err != nil {
		return nil, err
	}
	if bVer[2] != config.Default.ProtocolVersion {
		return nil, ErrWrongVersion
	}
	bLen, err := reader.Peek(7)
	if err != nil {
		return nil, err
	}
	pkLen := int(binary.BigEndian.Uint32(bLen[3:7]))
	if pkLen > reader.Len() {
		return nil, nil
	}
	if pkLen > MaxPacketSize {
		return nil, ErrPacketSizeExcced
	}
	btyp, err := reader.Peek(8)
	if err != nil {
		return nil, err
	}
	typ := Type(btyp[7])
	if typ < Heartbeat || typ >= Invalid {
		return nil, ErrWrongPacketType
	}
	r2, err := reader.Slice(pkLen)
	if err != nil {
		return nil, err
	}
	return r2, nil
}

func (p *Codec) Unpack1(reader netpoll.Reader) (*Packet, error) {
	offset := 0
	bMagic, err := reader.Next(2)
	if err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(bMagic) != config.Default.ProtocolMagic {
		return nil, ErrWrongMagic
	}
	offset += 2

	bVer, err := reader.Next(1)
	if err != nil {
		return nil, err
	}
	if bVer[0] != config.Default.ProtocolVersion {
		return nil, ErrWrongVersion
	}
	offset += 1

	bPkLen, err := reader.Next(4)
	if err != nil {
		return nil, err
	}
	pkLen := int(binary.BigEndian.Uint32(bPkLen))
	offset += 4

	btyp, err := reader.Next(1)
	if err != nil {
		return nil, err
	}
	typ := Type(btyp[0])
	offset += 1

	bId, err := reader.Next(4)
	if err != nil {
		return nil, err
	}
	id := int32(binary.BigEndian.Uint32(bId))
	offset += 4

	bCrc, err := reader.Next(4)
	if err != nil {
		return nil, err
	}
	expectCrc := binary.BigEndian.Uint32(bCrc)
	offset += 4

	var sid int64 = 0
	if p.isSidOffset(typ) {
		bsid, err := reader.Next(8)
		if err != nil {
			return nil, err
		}
		sid = int64(binary.BigEndian.Uint64(bsid))
		offset += 8
	}

	payload, _ := reader.Next(pkLen - offset)

	if config.Default.ProtocolEnableChecksum && crc32.ChecksumIEEE(payload) != expectCrc {
		_ = reader.Release()
		return nil, ErrChecksumMismatch
	}

	_ = reader.Release()
	switch typ {
	case InternalData, ClientData:
		return NewInternal(typ, id, sid, payload), nil
	default:
		return New(typ, id, payload), nil
	}
}

func (p *Codec) Unpack(data []byte) ([]*Packet, error) {
	if len(data) > 0 {
		if _, err := p.buf.Write(data); err != nil {
			return nil, err
		}
	}

	var packets []*Packet

	for {
		if p.size < 0 {

			if p.buf.Len() < HeadLength {
				break
			}

			b := p.buf.Bytes()[:HeadLength]
			if binary.BigEndian.Uint16(b[:2]) != config.Default.ProtocolMagic {
				return packets, ErrWrongMagic
			}
			if b[2] != config.Default.ProtocolVersion {
				return packets, ErrWrongVersion
			}
			pkLen := int32(binary.BigEndian.Uint32(b[3:7]))

			if p.buf.Len() < int(pkLen) {
				break
			}

			typ := Type(b[7])
			if typ < Heartbeat || typ >= Invalid {
				return packets, ErrWrongPacketType
			}

			offset := HeadLength
			if p.isSidOffset(typ) {
				offset += 8
			}

			id := int32(binary.BigEndian.Uint32(b[8:12]))
			expectCrc := binary.BigEndian.Uint32(b[12:16])

			p.buf.Next(HeadLength)

			if p.isSidOffset(typ) {
				p.sid = int64(binary.BigEndian.Uint64(p.buf.Next(8)))
			} else {
				p.sid = 0
			}

			payloadLen := int32(pkLen - int32(offset))
			if payloadLen < 0 || payloadLen > MaxPacketSize {
				return packets, ErrPacketSizeExcced
			}

			payload := p.buf.Next(int(payloadLen))

			if config.Default.ProtocolEnableChecksum && crc32.ChecksumIEEE(payload) != expectCrc {
				return packets, ErrChecksumMismatch
			}

			p.typ = typ
			p.Id = id
			p.size = payloadLen
		}

		if p.size == -1 || p.buf.Len() < int(p.size) {
			break
		}

		payload := p.buf.Next(int(p.size))

		var pkt *Packet
		switch p.typ {
		case InternalData, ClientData:
			pkt = NewInternal(p.typ, p.Id, p.sid, payload)
		default:
			pkt = New(p.typ, p.Id, payload)
		}

		packets = append(packets, pkt)

		p.size = -1
		p.sid = 0
		p.typ = 0
		p.Id = 0
	}

	return packets, nil
}
