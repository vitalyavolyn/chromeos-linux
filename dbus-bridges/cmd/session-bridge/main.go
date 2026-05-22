// session-bridge implements the ChromeOS session_manager pieces Ash expects.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	dbus "github.com/godbus/dbus/v5"
)

const (
	sessionService = "org.chromium.SessionManager"
	sessionPath    = dbus.ObjectPath("/org/chromium/SessionManager")
	sessionIface   = "org.chromium.SessionManagerInterface"

	udaService = "org.chromium.UserDataAuth"
	udaPath    = dbus.ObjectPath("/org/chromium/UserDataAuth")
	udaIface   = "org.chromium.UserDataAuthInterface"

	guestHome = "/home/chronos/user"
)

type sessionManager struct {
	conn    *dbus.Conn
	startCh chan struct{}
}

const (
	testAccountID = "linux@local"
	testUserHash  = "test-user"
)

func (s *sessionManager) EmitLoginPromptVisible() *dbus.Error {
	log.Println("EmitLoginPromptVisible — Ash login screen is up")
	select {
	case s.startCh <- struct{}{}:
	default:
	}
	return nil
}

func (s *sessionManager) EmitLoginPromptAnimationFinished() *dbus.Error {
	return nil
}

func (s *sessionManager) EmitAshInitialized() *dbus.Error {
	log.Println("EmitAshInitialized")
	return nil
}

func (s *sessionManager) EmitStartedUserSession(accountID string) *dbus.Error {
	log.Printf("EmitStartedUserSession: %s", accountID)
	return nil
}

func (s *sessionManager) StartSession(accountID, uniqueID string) *dbus.Error {
	log.Printf("StartSession: %s", accountID)
	return nil
}

func (s *sessionManager) StartSessionEx(accountID, uniqueID string, chromeOwnerKey bool) *dbus.Error {
	log.Printf("StartSessionEx: %s chrome_owner_key=%v", accountID, chromeOwnerKey)
	return nil
}

func (s *sessionManager) StopSession(uniqueID string) (bool, *dbus.Error) {
	log.Println("StopSession")
	return true, nil
}

func (s *sessionManager) StopSessionWithReason(reason uint32) *dbus.Error {
	log.Printf("StopSessionWithReason: %d", reason)
	return nil
}

func (s *sessionManager) LockScreen() *dbus.Error {
	return nil
}

func (s *sessionManager) RetrieveActiveSessions() (map[string]string, *dbus.Error) {
	return map[string]string{testAccountID: testUserHash}, nil
}

func (s *sessionManager) RetrievePrimarySession() (string, string, *dbus.Error) {
	return testAccountID, testUserHash, nil
}

func (s *sessionManager) RetrieveSessionState() (string, *dbus.Error) {
	return "started", nil
}

func (s *sessionManager) IsGuestSessionActive() (bool, *dbus.Error) {
	return false, nil
}

func (s *sessionManager) IsScreenLocked() (bool, *dbus.Error) {
	return false, nil
}

func (s *sessionManager) HandleLockScreenShown() *dbus.Error {
	return nil
}

func (s *sessionManager) HandleLockScreenDismissed() *dbus.Error {
	return nil
}

func (s *sessionManager) RetrievePolicyEx(descriptor []byte) ([]byte, *dbus.Error) {
	log.Printf("RetrievePolicyEx called (returning empty policy)")
	return []byte{}, nil
}

func (s *sessionManager) StorePolicyEx(descriptor []byte, policy []byte) *dbus.Error {
	log.Printf("StorePolicyEx called (ignored)")
	return nil
}

func (s *sessionManager) ListStoredComponentPolicies(descriptor []byte) ([]string, *dbus.Error) {
	return []string{}, nil
}

func boolField(fieldNum int, val bool) []byte {
	tag := byte((fieldNum << 3) | 0)
	if val {
		return []byte{tag, 0x01}
	}
	return []byte{tag, 0x00}
}

func bytesField(fieldNum int, val []byte) []byte {
	tag := byte((fieldNum << 3) | 2)
	l := byte(len(val))
	return append([]byte{tag, l}, val...)
}

type cryptohomeMisc struct{}

func (m *cryptohomeMisc) GetSystemSalt(req []byte) ([]byte, *dbus.Error) {
	salt := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0x01, 0x23, 0x45, 0x67}
	return bytesField(3, salt), nil
}

type userDataAuth struct{}

func (u *userDataAuth) IsMounted(req []byte) ([]byte, *dbus.Error) {
	return append(boolField(1, true), boolField(2, true)...), nil
}

func (u *userDataAuth) MountGuest(req []byte) ([]byte, *dbus.Error) {
	log.Println("MountGuest — creating", guestHome)
	os.MkdirAll(guestHome, 0755)
	return []byte{}, nil
}

func (u *userDataAuth) Unmount(req []byte) ([]byte, *dbus.Error) {
	log.Println("Unmount")
	return []byte{}, nil
}

func (u *userDataAuth) GetSupportedKeyPolicies(req []byte) ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (u *userDataAuth) StartAuthSession(req []byte) ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (u *userDataAuth) AuthenticateAuthSession(req []byte) ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (u *userDataAuth) GetAccountDiskUsage(req []byte) ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (u *userDataAuth) CheckHealth(req []byte) ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func main() {
	log.SetPrefix("[session-bridge] ")

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatalf("connect to system bus: %v", err)
	}
	defer conn.Close()

	reply, err := conn.RequestName(sessionService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", sessionService, err, reply)
	}

	startCh := make(chan struct{}, 1)
	mgr := &sessionManager{conn: conn, startCh: startCh}
	conn.Export(mgr, sessionPath, sessionIface)

	reply, err = conn.RequestName(udaService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", udaService, err, reply)
	}

	conn.Export(&userDataAuth{}, udaPath, udaIface)
	conn.Export(&cryptohomeMisc{}, udaPath, "org.chromium.CryptohomeMiscInterface")

	log.Printf("registered %s and %s", sessionService, udaService)

	go func() {
		for range startCh {
			time.Sleep(300 * time.Millisecond)
			if err := conn.Emit(sessionPath, sessionIface+".SessionStateChanged", "started"); err != nil {
				log.Printf("emit SessionStateChanged: %v", err)
			} else {
				log.Println("emitted SessionStateChanged(started)")
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
