package packet

import (
	"math"
	"testing"

	"github.com/cloudwego/netpoll"
)

func TestPackCodec(t *testing.T) {
	packCodec := NewPackCodec()
	b, err := packCodec.Pack(Heartbeat, 100, 1, []byte("helloworld"))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(len(b))
	bdata, err := packCodec.Pack(InternalData, math.MaxInt32, math.MaxInt64, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(bdata)

	ps, err := packCodec.Unpack(bdata)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(ps)
	bufdata := netpoll.NewLinkBuffer(1024)
	t.Log(bufdata.WriteBinary([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}))
	bufdata.Flush()
	t.Logf("% X", bufdata.Bytes())
}
