// mojo-stub: Mojo service manager socket for Chrome Ash.
//
// When IsRunningOnChromeOS()=true, Chrome connects to /run/mojo/service_manager.sock
// and expects an ACCEPT_INVITEE handshake. We perform that handshake then drain
// messages to keep Chrome alive.
//
// Wire format (from mojo/core/channel.h and node_channel.cc):
//
//	Channel::Header (16 bytes):
//	  [0:4]  num_bytes (uint32)
//	  [4:6]  num_header_bytes (uint16) — 16 on Linux
//	  [6:8]  message_type (uint16) — 0 = NORMAL
//	  [8:10] num_handles (uint16)
//	  [10:16] padding
//
//	NodeChannel::Header (8 bytes):
//	  [0:4]  type (uint32)
//	  [4:8]  padding
//
//	ACCEPT_INVITEE payload (AcceptInviteeDataV1, 40 bytes):
//	  [0:8]  inviter_name.v1 (uint64)
//	  [8:16] inviter_name.v2 (uint64)
//	  [16:24] token.v1 (uint64)
//	  [24:32] token.v2 (uint64)
//	  [32:40] capabilities (uint64) — 0 = kNodeCapabilityNone
package main

import (
	"encoding/binary"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"syscall"
)

const socketPath = "/run/mojo/service_manager.sock"

func main() {
	log.SetPrefix("[mojo-stub] ")

	os.MkdirAll("/run/mojo", 0755)
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen %s: %v", socketPath, err)
	}
	defer ln.Close()
	os.Chmod(socketPath, 0666)
	log.Printf("listening on %s", socketPath)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleChrome(conn.(*net.UnixConn))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}

func handleChrome(conn *net.UnixConn) {
	defer conn.Close()
	log.Printf("Chrome connected")

	// Build ACCEPT_INVITEE: Channel::Header (16) + NodeChannel::Header (8) + AcceptInviteeDataV1 (40)
	msg := make([]byte, 64)
	binary.LittleEndian.PutUint32(msg[0:], 64)
	binary.LittleEndian.PutUint16(msg[4:], 16)
	binary.LittleEndian.PutUint64(msg[24:], rand.Uint64())
	binary.LittleEndian.PutUint64(msg[32:], rand.Uint64())
	binary.LittleEndian.PutUint64(msg[40:], rand.Uint64())
	binary.LittleEndian.PutUint64(msg[48:], rand.Uint64())

	if _, err := conn.Write(msg); err != nil {
		log.Printf("send ACCEPT_INVITEE: %v", err)
		return
	}
	log.Printf("sent ACCEPT_INVITEE")

	io.Copy(io.Discard, conn)
	log.Printf("Chrome disconnected")
}
