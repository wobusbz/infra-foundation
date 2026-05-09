package cluster

import (
	"infra-foundation/protocol"

	"github.com/cloudwego/netpoll"
)

type netpollReader struct {
	r netpoll.Reader
}

func adaptNetpollReader(r netpoll.Reader) protocol.Reader {
	return &netpollReader{r: r}
}

func (n *netpollReader) Next(nbytes int) ([]byte, error)       { return n.r.Next(nbytes) }
func (n *netpollReader) Peek(nbytes int) ([]byte, error)       { return n.r.Peek(nbytes) }
func (n *netpollReader) Skip(nbytes int) error                 { return n.r.Skip(nbytes) }
func (n *netpollReader) ReadBinary(nbytes int) ([]byte, error) { return n.r.ReadBinary(nbytes) }
func (n *netpollReader) ReadString(nbytes int) (string, error) { return n.r.ReadString(nbytes) }
func (n *netpollReader) ReadByte() (byte, error)               { return n.r.ReadByte() }
func (n *netpollReader) Slice(nbytes int) (protocol.Reader, error) {
	r, err := n.r.Slice(nbytes)
	if err != nil {
		return nil, err
	}
	return &netpollReader{r: r}, nil
}
func (n *netpollReader) Release() error { return n.r.Release() }
func (n *netpollReader) Len() int       { return n.r.Len() }
