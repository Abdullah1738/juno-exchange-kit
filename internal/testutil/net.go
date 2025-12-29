package testutil

import (
	"fmt"
	"net"
)

func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok || addr.Port <= 0 {
		return 0, fmt.Errorf("free port: invalid addr %v", l.Addr())
	}
	return addr.Port, nil
}
