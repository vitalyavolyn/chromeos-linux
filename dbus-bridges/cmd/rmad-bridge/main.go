package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	dbus "github.com/godbus/dbus/v5"
)

const (
	rmadService = "org.chromium.Rmad"
	rmadPath    = dbus.ObjectPath("/org/chromium/Rmad")
	rmadIface   = "org.chromium.Rmad"
)

type rmadControl struct{}

func (r *rmadControl) IsRmaRequired() (bool, *dbus.Error) {
	return false, nil
}

func main() {
	log.SetPrefix("[rmad-bridge] ")

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatalf("connect to system bus: %v", err)
	}
	defer conn.Close()

	reply, err := conn.RequestName(rmadService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", rmadService, err, reply)
	}

	conn.Export(&rmadControl{}, rmadPath, rmadIface)
	log.Printf("registered %s", rmadService)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
