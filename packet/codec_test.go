package packet

import (
	"math"
	"testing"
)

func TestPackCodec(t *testing.T) {
	packCodec := NewPackCodec()
	b, err := packCodec.Pack(Heartbeat, 100, 0, []byte("helloworld"))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(len(b))
	bdata, err := packCodec.Pack(Heartbeat, 0, math.MaxInt64, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(len(bdata))
	ps, err := packCodec.Unpack(bdata)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(ps)
	for _, p := range ps {
		t.Log(p.String())
	}
}
