package localipaddr

import (
	"net"
	"strings"
)

func LocalIPAddr() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return strings.Split(conn.LocalAddr().(*net.UDPAddr).String(), ":")[0], nil
}
