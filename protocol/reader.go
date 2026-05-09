package protocol

type Reader interface {
	Next(n int) (p []byte, err error)
	Peek(n int) (buf []byte, err error)
	Skip(n int) error
	ReadBinary(n int) (p []byte, err error)
	ReadString(n int) (s string, err error)
	ReadByte() (b byte, err error)
	Slice(n int) (Reader, error)
	Release() error
	Len() int
}
