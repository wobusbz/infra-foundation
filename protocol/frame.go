package protocol

import (
	"encoding/binary"
	"errors"
)

var (
	ErrFrameInvalidLength = errors.New("frame: invalid length")
	ErrFrameTooLarge      = errors.New("frame: too large")
)

type FrameCodec struct {
	MinLen int
	MaxLen int
}

func NewFrameCodec(minLen, maxLen int) FrameCodec {
	return FrameCodec{MinLen: minLen, MaxLen: maxLen}
}

func (c FrameCodec) Next(reader Reader) (Reader, error) {
	if reader.Len() < 4 {
		return nil, nil
	}

	bLen, err := reader.Peek(4)
	if err != nil {
		return nil, err
	}

	total := int(binary.BigEndian.Uint32(bLen))
	if total < c.MinLen {
		return nil, ErrFrameInvalidLength
	}
	if c.MaxLen > 0 && total > c.MaxLen {
		return nil, ErrFrameTooLarge
	}
	if reader.Len() < total {
		return nil, nil
	}

	return reader.Slice(total)
}
