package packet

import (
	"bytes"
	"encoding/binary"
	"errors"

	"github.com/cloudwego/netpoll"
)

const (
	HeadLength    = 9
	MaxPacketSize = 10 << 20
)

var (
	ErrWrongPacketType  = errors.New("codec: wrong packet type")
	ErrPacketSizeExcced = errors.New("codec: packet size exceed")
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

func (p *PackCodec) Pack(typ Type, id int32, sid int64, payload []byte) ([]byte, error) {
	if typ < Heartbeat || typ >= Invalid {
		return nil, ErrWrongPacketType
	}
	payloadLen := uint32(len(payload))
	total := HeadLength
	if p.isSidOffset(typ) {
		total += 8
	}
	total += int(payloadLen)

	buf := make([]byte, total)

	binary.BigEndian.PutUint32(buf[:4], uint32(total))
	buf[4] = byte(typ)
	binary.BigEndian.PutUint32(buf[5:9], uint32(id))

	offset := HeadLength

	if p.isSidOffset(typ) {
		binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(sid))
		offset += 8
	}

	copy(buf[offset:], payload)
	return buf, nil
}

func (p *PackCodec) isSidOffset(typ Type) bool {
	return typ == ClientData || typ == InternalData
}

func (p *PackCodec) NextPacket(reader netpoll.Reader) (netpoll.Reader, error) {
	bLen, err := reader.Peek(4)
	if err != nil {
		if err == netpoll.ErrEOF {
			return nil, nil
		}
		return nil, err
	}
	pkLen := int(binary.BigEndian.Uint32(bLen))
	if pkLen > reader.Len() {
		return nil, nil
	}
	if pkLen > MaxPacketSize {
		return nil, ErrPacketSizeExcced
	}
	btyp, err := reader.Peek(5)
	if err != nil {
		return nil, err
	}
	typ := Type(btyp[4])
	if typ < Heartbeat || typ >= Invalid {
		return nil, ErrWrongPacketType
	}
	r2, err := reader.Slice(pkLen)
	if err != nil {
		return nil, err
	}
	return r2, nil
}

func (p *PackCodec) Unpack1(reader netpoll.Reader) (*Packet, error) {
	bPkLen, err := reader.Next(4)
	if err != nil {
		return nil, err
	}
	pkLen := int(binary.BigEndian.Uint32(bPkLen))

	btyp, err := reader.Next(1)
	if err != nil {
		return nil, err
	}

	typ := Type(btyp[0])

	bId, err := reader.Next(4)
	if err != nil {
		return nil, err
	}
	id := int32(binary.BigEndian.Uint32(bId))

	var offset int = HeadLength
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

	_ = reader.Release()
	switch typ {
	case InternalData, ClientData:
		return NewInternal(typ, id, sid, payload), nil
	default:
		return New(typ, id, payload), nil
	}
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
			pkLen := int32(binary.BigEndian.Uint32(b[:4]))

			if p.buf.Len() < int(pkLen) {
				break
			}

			typ := Type(b[4])
			if typ < Heartbeat || typ >= Invalid {
				return packets, ErrWrongPacketType
			}

			offset := HeadLength
			if p.isSidOffset(typ) {
				offset += 8
			}

			id := int32(binary.BigEndian.Uint32(b[5:9]))

			payloadLen := int32(pkLen - int32(offset))
			if payloadLen < 0 || payloadLen > MaxPacketSize {
				return packets, ErrPacketSizeExcced
			}

			p.buf.Next(HeadLength)

			if p.isSidOffset(typ) {
				p.sid = int64(binary.BigEndian.Uint64(p.buf.Next(8)))
			} else {
				p.sid = 0
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
