package cluster

import "infra-foundation/protocol"

type protocolBufReleaser struct{}

func (protocolBufReleaser) Release(buf []byte) {
	protocol.PutBuf(buf)
}
