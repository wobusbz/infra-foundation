package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const (
	HeadLength    = 8
	MaxPacketSize = 10 << 20
)

var (
	ErrPacketSizeExcced = errors.New("codec: packet size exceed")
	ErrPacketSizeExceed = errors.New("packet size exceed")
	ErrIncomplete       = errors.New("incomplete packet")
)

type PackCodec struct {
	buf  *bytes.Buffer
	size int32
	Id   int32
	sid  int64
	typ  Type
}

func NewPackCodec() *PackCodec {
	return &PackCodec{buf: bytes.NewBuffer(nil), size: -1}
}

func (p *PackCodec) Pack(pack *Packet) ([]byte, error) {
	if pack.typ < Heartbeat || (pack.typ >= Invalid) {
		return nil, ErrWrongPacketType
	}
	payloadLen := uint32(len(pack.data))
	total := HeadLength
	if p.isSidOffset(pack.typ) {
		total += 8
	}

	total += int(payloadLen)

	buf := make([]byte, total)

	buf[0] = byte(pack.typ)
	copy(buf[1:4], intToBytes(int(payloadLen)))
	binary.BigEndian.PutUint32(buf[4:8], uint32(pack.id))

	offset := HeadLength

	if p.isSidOffset(pack.typ) {
		binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(pack.sid))
		offset += 8
	}

	copy(buf[offset:], pack.data)
	return buf, nil
}

func (p *PackCodec) isSidOffset(typ Type) bool {
	return typ == ClientData || typ == InternalData
}

func (p *PackCodec) Unpack(data []byte) ([]*Packet, error) {
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

			typ := Type(b[0])
			if typ < Heartbeat || typ >= Invalid {
				return packets, ErrWrongPacketType
			}

			payloadLen := int32(bytesToInt(b[1:4]))
			if payloadLen < 0 || payloadLen > MaxPacketSize {
				return packets, ErrPacketSizeExceed
			}

			id := int32(binary.BigEndian.Uint32(b[4:8]))

			offset := HeadLength
			if p.isSidOffset(typ) {
				offset += 8
			}

			if p.buf.Len() < offset {
				break
			}

			if p.buf.Len() < offset+int(payloadLen) {
				break
			}

			p.buf.Next(HeadLength)

			if p.isSidOffset(typ) {
				sidBytes := p.buf.Next(8)
				p.sid = int64(binary.BigEndian.Uint64(sidBytes))
			} else {
				p.sid = 0
			}

			p.typ = typ
			p.Id = id
			p.size = payloadLen

		}

		if int(p.size) > p.buf.Len() {
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

func bytesToInt(b []byte) int {
	return int(b[2]) | int(b[1])<<8 | int(b[0])<<16
}

func intToBytes(n int) []byte {
	var b [3]byte
	b[0] = byte(n >> 16)
	b[1] = byte(n >> 8)
	b[2] = byte(n)
	return b[:]
}
