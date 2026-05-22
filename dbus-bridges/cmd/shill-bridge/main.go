// shill-bridge maps Ash networking calls onto NetworkManager.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	dbus "github.com/godbus/dbus/v5"
)

const (
	shillBusName = "org.chromium.flimflam"
	managerPath  = dbus.ObjectPath("/")
	managerIface = "org.chromium.flimflam.Manager"
	serviceIface = "org.chromium.flimflam.Service"
	deviceIface  = "org.chromium.flimflam.Device"
	profileIface = "org.chromium.flimflam.Profile"
)

func nmToShillState(nm uint32) string {
	switch nm {
	case 70:
		return "online" // NM_STATE_CONNECTED_GLOBAL
	case 60, 50:
		return "portal" // NM_STATE_CONNECTED_SITE / LOCAL
	case 40:
		return "association" // NM_STATE_CONNECTING
	default:
		return "offline"
	}
}

func nmAcToShillState(nm uint32) string {
	switch nm {
	case 2:
		return "online" // NM_ACTIVE_CONNECTION_STATE_ACTIVATED
	case 1:
		return "association"
	default:
		return "idle"
	}
}

type serviceInfo struct {
	path       dbus.ObjectPath
	connType   string
	name       string
	state      string
	strength   uint8
	guid       string
	devicePath string
	visible    bool
}

type deviceInfo struct {
	path     dbus.ObjectPath
	devType  string
	iface    string
	name     string
	powered  bool
	scanning bool
}

type shillManager struct {
	conn     *dbus.Conn
	mu       sync.RWMutex
	state    string
	services []serviceInfo
	devices  []deviceInfo

	tetheringActive  bool
	tetheringState   string
	tetheringClients int32
}

func (m *shillManager) GetProperties() (map[string]dbus.Variant, *dbus.Error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	paths := make([]dbus.ObjectPath, len(m.services))
	for i, s := range m.services {
		paths[i] = s.path
	}
	devicePaths := make([]dbus.ObjectPath, len(m.devices))
	for i, d := range m.devices {
		devicePaths[i] = d.path
	}

	techSet := map[string]struct{}{}
	for _, s := range m.services {
		techSet[s.connType] = struct{}{}
	}
	for _, d := range m.devices {
		techSet[d.devType] = struct{}{}
	}
	techs := make([]string, 0, len(techSet))
	for t := range techSet {
		techs = append(techs, t)
	}

	var defaultService string
	serviceComplete := make([]dbus.ObjectPath, len(m.services))
	for i, s := range m.services {
		serviceComplete[i] = s.path
		if s.state == "online" && defaultService == "" {
			defaultService = string(s.path)
		}
	}

	return map[string]dbus.Variant{
		"State":                 dbus.MakeVariant(m.state),
		"Profiles":              dbus.MakeVariant([]dbus.ObjectPath{"/profile/default"}),
		"Services":              dbus.MakeVariant(paths),
		"Devices":               dbus.MakeVariant(devicePaths),
		"EnabledTechnologies":   dbus.MakeVariant(techs),
		"AvailableTechnologies": dbus.MakeVariant(techs),
		"ConnectedTechnologies": dbus.MakeVariant(techs),
		"DefaultService":        dbus.MakeVariant(dbus.ObjectPath(defaultService)),
		"ServiceCompleteList":   dbus.MakeVariant(serviceComplete),
		"ConnectionState":       dbus.MakeVariant(m.state),
		"TetheringConfig": dbus.MakeVariant(map[string]dbus.Variant{
			"auto_disable":          dbus.MakeVariant(true),
			"band":                  dbus.MakeVariant("all-bands"),
			"security":              dbus.MakeVariant("wpa2"),
			"ssid":                  dbus.MakeVariant("746573745f746574686572696e675f73736964"),
			"passphrase":            dbus.MakeVariant("tetheringpassword"),
			"randomize_mac_address": dbus.MakeVariant(true),
		}),
		"TetheringCapabilities": dbus.MakeVariant(map[string]dbus.Variant{
			"upstream_technologies":   dbus.MakeVariant([]string{"cellular"}),
			"downstream_technologies": dbus.MakeVariant([]string{"wifi"}),
			"wifi_security_modes":     dbus.MakeVariant([]string{"wpa2"}),
		}),
		"TetheringStatus": tetheringStatusVariant(m.tetheringActive, m.tetheringState, m.tetheringClients),
		"P2PCapabilities": dbus.MakeVariant(map[string]dbus.Variant{
			"P2PSupported":      dbus.MakeVariant(true),
			"GroupReadiness":    dbus.MakeVariant("ready"),
			"ClientReadiness":   dbus.MakeVariant("ready"),
			"SupportedChannels": dbus.MakeVariant([]int32{1, 2}),
			"PreferredChannels": dbus.MakeVariant([]int32{1}),
		}),
	}, nil
}

func (m *shillManager) SetProperty(name string, value dbus.Variant) *dbus.Error {
	log.Printf("SetProperty: %s", name)
	return nil
}

func (m *shillManager) SetDNSProxyDOHProviders(dnsProxySetup map[string]dbus.Variant) *dbus.Error {
	log.Printf("SetDNSProxyDOHProviders: %v", dnsProxySetup)
	return nil
}

func (m *shillManager) ConnectService(props map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return "/service/0", nil
}

func (m *shillManager) GetDebugLevel() (string, *dbus.Error) {
	return "off", nil
}

func (m *shillManager) EnableTethering(priority string) (string, *dbus.Error) {
	m.mu.RLock()
	devices := m.devices
	m.mu.RUnlock()

	var wifiPath string
	for _, d := range devices {
		if d.devType == "wifi" {
			wifiPath = d.name
			break
		}
	}
	if wifiPath == "" {
		return "", dbus.NewError("org.chromium.flimflam.Error.InternalError",
			[]any{"no wifi device available for hotspot"})
	}

	m.mu.Lock()
	m.tetheringActive = true
	m.tetheringState = "starting"
	m.tetheringClients = 0
	m.mu.Unlock()
	log.Printf("EnableTethering priority=%s", priority)

	m.conn.Emit(managerPath, managerIface+".PropertyChanged",
		"TetheringStatus", m.tetheringStatusLocked())

	go m.createNMHotspot()
	return "success", nil
}

func (m *shillManager) DisableTethering() (string, *dbus.Error) {
	m.mu.Lock()
	m.tetheringActive = false
	m.tetheringState = "idle"
	m.mu.Unlock()
	log.Printf("DisableTethering")

	m.conn.Emit(managerPath, managerIface+".PropertyChanged",
		"TetheringStatus", m.tetheringStatusLocked())

	go m.deactivateNMHotspot()
	return "success", nil
}

func (m *shillManager) CheckTetheringReadiness() (string, *dbus.Error) {
	m.mu.RLock()
	devices := m.devices
	m.mu.RUnlock()

	for _, d := range devices {
		if d.devType == "wifi" {
			return "ready", nil
		}
	}
	return "", dbus.NewError("org.chromium.flimflam.Error.InternalError",
		[]any{"no wifi device"})
}

func (m *shillManager) tetheringStatusLocked() dbus.Variant {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return tetheringStatusVariant(m.tetheringActive, m.tetheringState, m.tetheringClients)
}

func tetheringStatusVariant(active bool, state string, clients int32) dbus.Variant {
	if !active || state == "idle" {
		return dbus.MakeVariant(map[string]dbus.Variant{
			"state":       dbus.MakeVariant("idle"),
			"idle_reason": dbus.MakeVariant("user_exit"),
		})
	}
	m := map[string]dbus.Variant{
		"state": dbus.MakeVariant(state),
	}
	if state == "active" {
		m["clients"] = dbus.MakeVariant([]map[string]dbus.Variant{})
	}
	return dbus.MakeVariant(m)
}

func (m *shillManager) findWifiDevicePath() (dbus.ObjectPath, error) {
	nm := m.conn.Object("org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager")
	devsVar, err := getPropVariant(nm, "org.freedesktop.NetworkManager", "Devices")
	if err != nil {
		return "", err
	}
	paths, _ := devsVar.Value().([]dbus.ObjectPath)
	for _, p := range paths {
		dev := m.conn.Object("org.freedesktop.NetworkManager", p)
		t, _ := getPropVariant(dev, "org.freedesktop.NetworkManager.Device", "DeviceType")
		if dt, _ := t.Value().(uint32); dt == 2 {
			return p, nil
		}
	}
	return "", fmt.Errorf("no wifi device")
}

func (m *shillManager) createNMHotspot() {
	wifiPath, err := m.findWifiDevicePath()
	if err != nil {
		log.Printf("hotspot: %v", err)
		m.mu.Lock()
		m.tetheringState = "idle"
		m.tetheringActive = false
		m.mu.Unlock()
		m.conn.Emit(managerPath, managerIface+".PropertyChanged",
			"TetheringStatus", m.tetheringStatusLocked())
		return
	}

	ssid := []byte("ChromeOS Hotspot")
	settings := map[string]map[string]dbus.Variant{
		"connection": {
			"type": dbus.MakeVariant("802-11-wireless"),
			"id":   dbus.MakeVariant("ChromeOS Hotspot"),
		},
		"802-11-wireless": {
			"mode": dbus.MakeVariant("ap"),
			"ssid": dbus.MakeVariant(ssid),
		},
		"802-11-wireless-security": {
			"key-mgmt": dbus.MakeVariant("wpa-psk"),
			"psk":      dbus.MakeVariant("tetheringpassword"),
		},
		"ipv4": {
			"method": dbus.MakeVariant("shared"),
		},
		"ipv6": {
			"method": dbus.MakeVariant("ignore"),
		},
	}

	var conPath dbus.ObjectPath
	nm := m.conn.Object("org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager")
	err = nm.Call("org.freedesktop.NetworkManager.AddAndActivateConnection", 0,
		settings, wifiPath, dbus.ObjectPath("/")).Store(&conPath)
	if err != nil {
		log.Printf("hotspot: AddAndActivateConnection failed: %v", err)
		m.mu.Lock()
		m.tetheringState = "idle"
		m.tetheringActive = false
		m.mu.Unlock()
		m.conn.Emit(managerPath, managerIface+".PropertyChanged",
			"TetheringStatus", m.tetheringStatusLocked())
		return
	}

	log.Printf("hotspot: active at %s", conPath)

	m.mu.Lock()
	m.tetheringState = "active"
	m.mu.Unlock()
	m.conn.Emit(managerPath, managerIface+".PropertyChanged",
		"TetheringStatus", m.tetheringStatusLocked())
}

func (m *shillManager) deactivateNMHotspot() {
	nm := m.conn.Object("org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager")

	acVar, err := getPropVariant(nm, "org.freedesktop.NetworkManager", "ActiveConnections")
	if err != nil {
		log.Printf("hotspot: can't list active connections: %v", err)
		return
	}
	acPaths, _ := acVar.Value().([]dbus.ObjectPath)
	for _, acPath := range acPaths {
		ac := m.conn.Object("org.freedesktop.NetworkManager", acPath)
		id, _ := getPropVariant(ac, "org.freedesktop.NetworkManager.Connection.Active", "Id")
		if name, _ := id.Value().(string); name == "ChromeOS Hotspot" {
			nm.Call("org.freedesktop.NetworkManager.DeactivateConnection", 0, acPath)
			log.Printf("hotspot: deactivated %s", acPath)
			return
		}
	}
	log.Printf("hotspot: no active hotspot connection found")
}

type shillService struct {
	info serviceInfo
}

func (s *shillService) GetProperties() (map[string]dbus.Variant, *dbus.Error) {
	props := map[string]dbus.Variant{
		"GUID":        dbus.MakeVariant(s.info.guid),
		"Type":        dbus.MakeVariant(s.info.connType),
		"State":       dbus.MakeVariant(s.info.state),
		"Name":        dbus.MakeVariant(s.info.name),
		"Strength":    dbus.MakeVariant(s.info.strength),
		"Device":      dbus.MakeVariant(s.info.devicePath),
		"Visible":     dbus.MakeVariant(s.info.visible),
		"Profile":     dbus.MakeVariant("/profile/default"),
		"AutoConnect": dbus.MakeVariant(true),
	}
	if s.info.connType == "ethernet" {
		props["Connectable"] = dbus.MakeVariant(true)
	}
	if s.info.connType == "wifi" {
		props["Connectable"] = dbus.MakeVariant(s.info.state != "idle")
		props["SecurityClass"] = dbus.MakeVariant("none")
		props["Mode"] = dbus.MakeVariant("managed")
		props["SSID"] = dbus.MakeVariant(s.info.name)
	}
	return props, nil
}

type shillDevice struct {
	info deviceInfo
}

func (d *shillDevice) GetProperties() (map[string]dbus.Variant, *dbus.Error) {
	return map[string]dbus.Variant{
		"Type":      dbus.MakeVariant(d.info.devType),
		"Interface": dbus.MakeVariant(d.info.iface),
		"Name":      dbus.MakeVariant(d.info.name),
		"Powered":   dbus.MakeVariant(d.info.powered),
		"Scanning":  dbus.MakeVariant(d.info.scanning),
	}, nil
}

func (d *shillDevice) SetProperty(name string, value dbus.Variant) *dbus.Error {
	log.Printf("Device.SetProperty: %s", name)
	return nil
}

func (d *shillDevice) RequestScan() *dbus.Error {
	log.Printf("Device.RequestScan: %s", d.info.path)
	return nil
}

type shillProfile struct{}

func (p *shillProfile) GetProperties() (map[string]dbus.Variant, *dbus.Error) {
	return map[string]dbus.Variant{
		"Entries":      dbus.MakeVariant(map[string]dbus.Variant{}),
		"UserConsumed": dbus.MakeVariant(false),
	}, nil
}

func (p *shillProfile) GetEntry(name string) (map[string]dbus.Variant, *dbus.Error) {
	return nil, dbus.NewError("org.chromium.flimflam.Error.InvalidArgs", []any{"entry not found"})
}

func (p *shillProfile) DeleteEntry(name string) *dbus.Error {
	return nil
}

func getPropVariant(obj dbus.BusObject, iface, prop string) (dbus.Variant, error) {
	var v dbus.Variant
	err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, iface, prop).Store(&v)
	return v, err
}

func queryNM(conn *dbus.Conn) (string, []serviceInfo, []deviceInfo, error) {
	nm := conn.Object("org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager")

	stateVar, err := getPropVariant(nm, "org.freedesktop.NetworkManager", "State")
	if err != nil {
		return "offline", nil, nil, fmt.Errorf("NM state: %w", err)
	}
	managerState := nmToShillState(stateVar.Value().(uint32))

	devices, nmDevicePaths := queryNMDevices(conn, nm)
	nmDeviceIndex := make(map[dbus.ObjectPath]int)
	for i, p := range nmDevicePaths {
		nmDeviceIndex[p] = i
	}

	acVar, err := getPropVariant(nm, "org.freedesktop.NetworkManager", "ActiveConnections")
	if err != nil {
		return managerState, nil, devices, nil
	}
	activePaths, _ := acVar.Value().([]dbus.ObjectPath)

	var services []serviceInfo
	svcIdx := 0
	for _, acPath := range activePaths {
		skip := false
		ac := conn.Object("org.freedesktop.NetworkManager", acPath)

		typeVar, _ := getPropVariant(ac, "org.freedesktop.NetworkManager.Connection.Active", "Type")
		rawType, _ := typeVar.Value().(string)
		var connType string
		switch {
		case strings.Contains(rawType, "wireless") || strings.Contains(rawType, "wifi"):
			connType = "wifi"
		case rawType == "802-3-ethernet" || rawType == "ethernet":
			connType = "ethernet"
		default:
			skip = true
		}

		idVar, _ := getPropVariant(ac, "org.freedesktop.NetworkManager.Connection.Active", "Id")
		name, _ := idVar.Value().(string)

		stVar, _ := getPropVariant(ac, "org.freedesktop.NetworkManager.Connection.Active", "State")
		acState, _ := stVar.Value().(uint32)

		devsVar, _ := getPropVariant(ac, "org.freedesktop.NetworkManager.Connection.Active", "Devices")
		devPaths, _ := devsVar.Value().([]dbus.ObjectPath)
		var devPath string
		if len(devPaths) > 0 {
			if idx, ok := nmDeviceIndex[devPaths[0]]; ok {
				devPath = fmt.Sprintf("/device/%d", idx)
			}
		}
		var strength uint8
		if connType == "wifi" {
			strength = wifiStrength(conn, devPaths)
		}

		if skip {
			continue
		}

		services = append(services, serviceInfo{
			path:       dbus.ObjectPath(fmt.Sprintf("/service/%d", svcIdx)),
			connType:   connType,
			name:       name,
			state:      nmAcToShillState(acState),
			strength:   strength,
			guid:       fmt.Sprintf("%s_guid", name),
			devicePath: devPath,
			visible:    true,
		})
		svcIdx++
	}

	log.Printf("NM state=%s services=%d", managerState, len(services))
	return managerState, services, devices, nil
}

func queryNMDevices(conn *dbus.Conn, nm dbus.BusObject) ([]deviceInfo, []dbus.ObjectPath) {
	devVar, err := getPropVariant(nm, "org.freedesktop.NetworkManager", "Devices")
	if err != nil {
		return nil, nil
	}
	nmPaths, _ := devVar.Value().([]dbus.ObjectPath)

	devices := make([]deviceInfo, 0, len(nmPaths))
	matched := make([]dbus.ObjectPath, 0, len(nmPaths))
	for _, nmPath := range nmPaths {
		dev := conn.Object("org.freedesktop.NetworkManager", nmPath)

		typeVar, err := getPropVariant(dev, "org.freedesktop.NetworkManager.Device", "DeviceType")
		if err != nil {
			continue
		}
		devTypeNum, _ := typeVar.Value().(uint32)
		devType := nmDeviceTypeToShill(devTypeNum)
		if devType == "" {
			continue
		}

		ifaceVar, _ := getPropVariant(dev, "org.freedesktop.NetworkManager.Device", "Interface")
		iface, _ := ifaceVar.Value().(string)
		j := len(devices)
		name := iface
		if name == "" {
			name = fmt.Sprintf("%s%d", devType, j)
		}

		devices = append(devices, deviceInfo{
			path:    dbus.ObjectPath(fmt.Sprintf("/device/%d", j)),
			devType: devType,
			iface:   iface,
			name:    name,
			powered: true,
		})
		matched = append(matched, nmPath)
	}
	return devices, matched
}

func nmDeviceTypeToShill(nm uint32) string {
	switch nm {
	case 1:
		return "ethernet"
	case 2:
		return "wifi"
	default:
		return ""
	}
}

func wifiStrength(conn *dbus.Conn, devPaths []dbus.ObjectPath) uint8 {
	for _, path := range devPaths {
		dev := conn.Object("org.freedesktop.NetworkManager", path)
		apVar, err := getPropVariant(dev, "org.freedesktop.NetworkManager.Device.Wireless", "ActiveAccessPoint")
		if err != nil {
			continue
		}
		apPath, _ := apVar.Value().(dbus.ObjectPath)
		if apPath == "/" || apPath == "" {
			continue
		}
		ap := conn.Object("org.freedesktop.NetworkManager", apPath)
		sv, _ := getPropVariant(ap, "org.freedesktop.NetworkManager.AccessPoint", "Strength")
		if s, ok := sv.Value().(uint8); ok {
			return s
		}
	}
	return 0
}

func syncServiceObjects(conn *dbus.Conn, services []serviceInfo) {
	for _, svc := range services {
		svc := svc
		conn.Export(&shillService{info: svc}, svc.path, serviceIface)
	}
}

func syncDeviceObjects(conn *dbus.Conn, devices []deviceInfo) {
	for _, dev := range devices {
		dev := dev
		conn.Export(&shillDevice{info: dev}, dev.path, deviceIface)
	}
}

func main() {
	log.SetPrefix("[shill-bridge] ")

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatalf("connect to system bus: %v", err)
	}
	defer conn.Close()

	reply, err := conn.RequestName(shillBusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", shillBusName, err, reply)
	}

	mgr := &shillManager{conn: conn, state: "offline"}
	conn.Export(mgr, managerPath, managerIface)

	conn.Export(&shillProfile{}, "/profile/default", profileIface)

	log.Printf("registered %s", shillBusName)

	if state, svcs, devs, err := queryNM(conn); err != nil {
		log.Printf("NM not available: %v", err)
	} else {
		mgr.mu.Lock()
		mgr.state = state
		mgr.services = svcs
		mgr.devices = devs
		mgr.mu.Unlock()
		syncServiceObjects(conn, svcs)
		syncDeviceObjects(conn, devs)
	}

	go func() {
		lastState := ""
		lastSvcCount := -1
		lastDevCount := -1
		for range time.Tick(5 * time.Second) {
			state, svcs, devs, err := queryNM(conn)
			if err != nil {
				continue
			}
			mgr.mu.Lock()
			mgr.state = state
			mgr.services = svcs
			mgr.devices = devs
			mgr.mu.Unlock()
			syncServiceObjects(conn, svcs)
			syncDeviceObjects(conn, devs)

			paths := make([]dbus.ObjectPath, len(svcs))
			for i, s := range svcs {
				paths[i] = s.path
			}
			devicePaths := make([]dbus.ObjectPath, len(devs))
			for i, d := range devs {
				devicePaths[i] = d.path
			}

			if state != lastState {
				conn.Emit(managerPath, managerIface+".PropertyChanged", "State", dbus.MakeVariant(state))
				lastState = state
			}
			if len(svcs) != lastSvcCount {
				conn.Emit(managerPath, managerIface+".PropertyChanged", "Services", dbus.MakeVariant(paths))
				lastSvcCount = len(svcs)
			}
			if len(devs) != lastDevCount {
				conn.Emit(managerPath, managerIface+".PropertyChanged", "Devices", dbus.MakeVariant(devicePaths))
				lastDevCount = len(devs)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
