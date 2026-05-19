//go:build linux

package runtime

import (
	"net"
	"os"
)

func SdNotify(state string) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	addr := &net.UnixAddr{Name: socket, Net: "unixgram"}
	if len(socket) > 0 && socket[0] == '@' {
		// Abstract namespace socket.
		addr.Name = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}
