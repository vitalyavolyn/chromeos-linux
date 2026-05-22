// crostini-bridge exposes local Linux desktop apps through Ash's Crostini path.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	dbus "github.com/godbus/dbus/v5"
)

func xdgRuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

const (
	conciergeBus    = "org.chromium.VmConcierge"
	ciceronebus     = "org.chromium.VmCicerone"
	dlcServiceBus   = "org.chromium.DlcService"
	debugdBus       = "org.chromium.debugd"
	conciergeIface  = "org.chromium.VmConcierge"
	ciceroneIface   = "org.chromium.VmCicerone"
	dlcServiceIface = "org.chromium.DlcServiceInterface"
	debugdIface     = "org.chromium.debugd"
	conciergeObj    = dbus.ObjectPath("/org/chromium/VmConcierge")
	ciceroneObj     = dbus.ObjectPath("/org/chromium/VmCicerone")
	dlcServiceObj   = dbus.ObjectPath("/org/chromium/DlcService")
	debugdObj       = dbus.ObjectPath("/org/chromium/debugd")
	vmName          = "termina"
	containerName   = "penguin"
	containerUser   = "user"
	containerToken  = "termina-penguin"

	imageLoaderBus   = "org.chromium.ImageLoader"
	imageLoaderIface = "org.chromium.ImageLoaderInterface"
	imageLoaderObj   = dbus.ObjectPath("/org/chromium/ImageLoader")
	terminaMount     = "/run/imageloader/cros-termina/99999.0.0"
)

var appDirs []string

func init() {
	home := os.Getenv("HOME")
	user := os.Getenv("USER")
	appDirs = []string{
		"/run/current-system/sw/share/applications",
		"/usr/share/applications",
		"/usr/local/share/applications",
		"/etc/xdg/applications",
	}
	if home != "" {
		appDirs = append(appDirs, home+"/.local/share/applications")
	}
	if user != "" {
		appDirs = append(appDirs, "/etc/profiles/per-user/"+user+"/share/applications")
	}

	extraPaths := []string{
		"/run/current-system/sw/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	}
	if home != "" {
		extraPaths = append(extraPaths, home+"/.nix-profile/bin")
	}
	if user != "" {
		extraPaths = append(extraPaths, "/etc/profiles/per-user/"+user+"/bin")
	}
	envPath := os.Getenv("PATH")
	seen := map[string]bool{}
	var parts []string
	for _, p := range append(strings.Split(envPath, ":"), extraPaths...) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	appPath = strings.Join(parts, ":")
}

var appPath string

const (
	dlcErrorNone         = "org.chromium.DlcServiceInterface.NONE"
	dlcErrorNoImageFound = "org.chromium.DlcServiceInterface.NO_IMAGE_FOUND"
)

var owner = struct {
	sync.RWMutex
	id string
}{id: "test-user"}

var session = struct {
	sync.RWMutex
	ready bool
}{}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendField(b []byte, field int, wireType int) []byte {
	return appendVarint(b, uint64(field<<3|wireType))
}

func appendString(b []byte, field int, s string) []byte {
	b = appendField(b, field, 2)
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

func appendBytes(b []byte, field int, v []byte) []byte {
	b = appendField(b, field, 2)
	b = appendVarint(b, uint64(len(v)))
	return append(b, v...)
}

func appendBool(b []byte, field int, v bool) []byte {
	b = appendField(b, field, 0)
	if v {
		return append(b, 1)
	}
	return append(b, 0)
}

func appendUint64(b []byte, field int, v uint64) []byte {
	b = appendField(b, field, 0)
	return appendVarint(b, v)
}

func currentOwnerID() string {
	owner.RLock()
	defer owner.RUnlock()
	return owner.id
}

func sessionReady() bool {
	session.RLock()
	defer session.RUnlock()
	return session.ready
}

func markSessionReady() {
	session.Lock()
	session.ready = true
	session.Unlock()
}

func markSessionStopped() {
	session.Lock()
	session.ready = false
	session.Unlock()
}

func setOwnerID(method, v string) {
	if v == "" || v == vmName || v == containerName || v == containerUser ||
		v == "borealis" || v == "bruschetta" || v == "baguette" {
		return
	}
	owner.Lock()
	if owner.id != v {
		log.Printf("%s: learned owner_id=%q", method, v)
		owner.id = v
	}
	owner.Unlock()
}

func learnOwnerID(method string, fields map[uint64]string) {
	for _, field := range []uint64{2, 1, 7, 5, 3} {
		if v := fields[field]; v != "" {
			setOwnerID(method, v)
			return
		}
	}
}

func encodeContainerStarted() []byte {
	b := appendString(nil, 1, vmName)
	b = appendString(b, 2, containerName)
	b = appendString(b, 3, currentOwnerID())
	b = appendString(b, 4, containerUser) // field 4 = container_username
	b = appendString(b, 5, "/home/user")
	b = appendString(b, 6, "127.0.0.1")
	b = appendString(b, 8, containerToken)
	return b
}

func encodeLaunchResponse(success bool, reason string) []byte {
	b := appendBool(nil, 1, success)
	if reason != "" {
		b = appendString(b, 2, reason)
	}
	return b
}

func encodeVmInfo() []byte {
	b := appendUint64(nil, 3, 4) // cid
	b = appendUint64(b, 6, 1)    // vm_type = TERMINA
	b = appendUint64(b, 8, 1)    // status = VM_STATUS_RUNNING
	return b
}

func encodeGetVmInfoResponse(success bool) []byte {
	b := appendBool(nil, 1, success)
	if success {
		b = appendBytes(b, 2, encodeVmInfo())
	}
	return b
}

func encodeListVmsResponse() []byte {
	vm := appendString(nil, 1, vmName)
	vm = appendString(vm, 2, currentOwnerID())
	vm = appendBytes(vm, 3, encodeVmInfo())
	vm = appendUint64(vm, 4, 1) // VM_STATUS_RUNNING

	b := appendBool(nil, 1, true)
	b = appendBytes(b, 3, vm)
	return b
}

func encodeListVmDisksResponse() []byte {
	disk := appendString(nil, 1, vmName)
	disk = appendUint64(disk, 2, 0) // storage_location = STORAGE_CRYPTOHOME_ROOT
	disk = appendUint64(disk, 3, 0) // size
	disk = appendUint64(disk, 5, 1) // image_type = DISK_IMAGE_QCOW2
	disk = appendString(disk, 7, "/run/daemon-store/crosvm/test-user/termina.img")
	disk = appendUint64(disk, 9, 1) // vm_type = TERMINA
	disk = appendBool(disk, 10, true)

	b := appendBool(nil, 1, true)
	b = appendBytes(b, 2, disk)
	b = appendUint64(b, 4, 1)
	return b
}

func encodeListRunningContainersResponse() []byte {
	info := appendString(nil, 1, vmName)
	info = appendString(info, 2, containerName)
	info = appendString(info, 4, containerToken)
	return appendBytes(nil, 1, info)
}

func encodeStartVmResponse() []byte {
	b := appendBool(nil, 1, true) // success = true
	b = appendUint64(b, 4, 1)     // status = VM_STATUS_RUNNING (skip wait for TremplinStarted)
	return b
}

func encodeVmStartedSignal() []byte {
	vmId := appendString(nil, 1, currentOwnerID())
	vmId = appendString(vmId, 2, vmName)
	b := appendBytes(nil, 1, vmId)
	b = appendUint64(b, 3, 1) // status = VM_STATUS_RUNNING
	return b
}

func encodeCreateDiskImageResponse() []byte {
	b := appendUint64(nil, 1, 2)                                             // status = DISK_STATUS_EXISTS
	b = appendString(b, 2, "/run/daemon-store/crosvm/test-user/termina.img") // disk_path = field 2
	return b
}

func decodeVarint(b []byte) (uint64, int) {
	var x uint64
	for s := uint(0); s < 64 && len(b) > int(s/7); s += 7 {
		byt := b[s/7]
		x |= uint64(byt&0x7f) << s
		if byt < 0x80 {
			return x, int(s/7) + 1
		}
	}
	return 0, 0
}

func decodeStringFields(data []byte) map[uint64]string {
	fields := map[uint64]string{}
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		wireType := tag & 0x7
		fieldNum := tag >> 3
		switch wireType {
		case 0:
			_, n = decodeVarint(data[i:])
			i += n
		case 2:
			length, n := decodeVarint(data[i:])
			i += n
			end := i + int(length)
			if end <= len(data) {
				s := string(data[i:end])
				if utf8.ValidString(s) {
					fields[fieldNum] = s
				}
			}
			i = end
		case 1:
			i += 8
		case 5:
			i += 4
		default:
			return fields
		}
	}
	return fields
}

func decodeUint64Fields(data []byte) map[uint64]uint64 {
	fields := map[uint64]uint64{}
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		wireType := tag & 0x7
		fieldNum := tag >> 3
		switch wireType {
		case 0:
			v, n := decodeVarint(data[i:])
			i += n
			fields[fieldNum] = v
		case 2:
			length, n := decodeVarint(data[i:])
			i += n
			i += int(length)
		case 1:
			i += 8
		case 5:
			i += 4
		default:
			return fields
		}
	}
	return fields
}

func decodeDesktopFileID(data []byte) string {
	return decodeStringFields(data)[3]
}

type desktopApp struct {
	id   string
	name string
	exec string
}

func parseDesktop(path, id string) (desktopApp, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return desktopApp{}, false
	}
	var name, execLine, appType string
	inEntry := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "[Desktop Entry]" {
			inEntry = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inEntry = false
			continue
		}
		if !inEntry {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Name":
			if name == "" {
				name = strings.TrimSpace(v)
			}
		case "Exec":
			execLine = strings.TrimSpace(v)
		case "Type":
			appType = strings.TrimSpace(v)
		case "NoDisplay":
			if strings.EqualFold(strings.TrimSpace(v), "true") {
				return desktopApp{}, false
			}
		}
	}
	if appType != "Application" || name == "" {
		return desktopApp{}, false
	}
	return desktopApp{id: id, name: name, exec: execLine}, true
}

func scanApps() []desktopApp {
	seen := map[string]bool{}
	var apps []desktopApp
	for _, dir := range appDirs {
		log.Printf("scanApps: checking dir %q", dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("scanApps: dir %q error: %v", dir, err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".desktop")
			if seen[id] {
				continue
			}
			if app, ok := parseDesktop(filepath.Join(dir, e.Name()), id); ok {
				seen[id] = true
				apps = append(apps, app)
			}
		}
	}
	log.Printf("scanned %d apps (after dedup)", len(apps))
	for _, a := range apps {
		log.Printf("  app: %s", a.id)
	}
	return apps
}

func findApp(id string) (desktopApp, bool) {
	for _, dir := range appDirs {
		if app, ok := parseDesktop(filepath.Join(dir, id+".desktop"), id); ok {
			return app, true
		}
	}
	return desktopApp{}, false
}

func launchApp(app desktopApp) error {
	execStr := app.exec
	for _, code := range []string{"%f", "%F", "%u", "%U", "%i", "%c", "%k", "%d", "%D", "%n", "%N", "%v", "%m"} {
		execStr = strings.ReplaceAll(execStr, code, "")
	}
	parts := strings.Fields(strings.TrimSpace(execStr))
	if len(parts) == 0 {
		return nil
	}
	program := parts[0]
	if !strings.Contains(program, "/") {
		for _, dir := range strings.Split(appPath, ":") {
			candidate := filepath.Join(dir, program)
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				program = candidate
				break
			}
		}
	}
	cmd := exec.Command(program, parts[1:]...)
	overrides := []string{
		"DISPLAY=:10",
		"WAYLAND_DISPLAY=wayland-0",
		"PATH=" + appPath,
		"GDK_BACKEND=x11",
		"QT_QPA_PLATFORM=xcb",
		"XDG_DATA_DIRS=/run/opengl-driver/share:/run/current-system/sw/share:/usr/share",
		"XDG_RUNTIME_DIR=" + xdgRuntimeDir(),
	}
	cmd.Env = append(overrides, os.Environ()...)
	return cmd.Start()
}

type vmConcierge struct {
	conn *dbus.Conn
}

func (v *vmConcierge) StartVm(req []byte, fds []dbus.UnixFD) ([]byte, *dbus.Error) {
	log.Printf("StartVm called fd_count=%d", len(fds))
	markSessionReady()
	go func() {
		time.Sleep(200 * time.Millisecond)
		v.conn.Emit(conciergeObj, conciergeIface+".VmStartedSignal", encodeVmStartedSignal())
		log.Printf("StartVm: emitted VmStartedSignal")
		time.Sleep(800 * time.Millisecond)
		v.conn.Emit(ciceroneObj, ciceroneIface+".ContainerStarted", encodeContainerStarted())
		log.Printf("StartVm: emitted ContainerStarted")
	}()
	return encodeStartVmResponse(), nil
}

func (v *vmConcierge) GetVmLaunchAllowed(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("GetVmLaunchAllowed", decodeStringFields(req))
	log.Printf("GetVmLaunchAllowed called")
	return appendBool(nil, 1, true), nil // allowed = true
}

func (v *vmConcierge) ListVms(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("ListVms", decodeStringFields(req))
	log.Printf("ListVms called")
	return encodeListVmsResponse(), nil
}

func (v *vmConcierge) ListVmDisks(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("ListVmDisks", decodeStringFields(req))
	log.Printf("ListVmDisks called")
	return encodeListVmDisksResponse(), nil
}

func (v *vmConcierge) GetVmInfo(req []byte) ([]byte, *dbus.Error) {
	fields := decodeStringFields(req)
	learnOwnerID("GetVmInfo", fields)
	name := fields[1]
	success := name == vmName && sessionReady()
	log.Printf("GetVmInfo called name=%q sessionReady=%v", name, success)
	return encodeGetVmInfoResponse(success), nil
}

func (v *vmConcierge) StopVm(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("StopVm", decodeStringFields(req))
	log.Printf("StopVm called")
	markSessionStopped()
	return appendBool(nil, 1, true), nil // success = true
}

func (v *vmConcierge) CreateDiskImage(req []byte, fds []dbus.UnixFD) ([]byte, *dbus.Error) {
	learnOwnerID("CreateDiskImage", decodeStringFields(req))
	log.Printf("CreateDiskImage called fd_count=%d", len(fds))
	return encodeCreateDiskImageResponse(), nil
}

type vmCicerone struct {
	mu   sync.RWMutex
	apps []desktopApp
}

func (c *vmCicerone) ListRunningContainers(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("ListRunningContainers", decodeStringFields(req))
	log.Printf("ListRunningContainers called")
	return encodeListRunningContainersResponse(), nil
}

func (c *vmCicerone) LaunchContainerApplication(req []byte) ([]byte, *dbus.Error) {
	fields := decodeStringFields(req)
	setOwnerID("LaunchContainerApplication", fields[4])
	id := fields[3]
	log.Printf("LaunchContainerApplication: desktop_file_id=%q", id)
	if id == "" {
		return encodeLaunchResponse(false, "empty desktop_file_id"), nil
	}
	app, ok := findApp(id)
	if !ok {
		return encodeLaunchResponse(false, "desktop file not found: "+id), nil
	}
	if err := launchApp(app); err != nil {
		log.Printf("launch %s: %v", id, err)
		return encodeLaunchResponse(false, err.Error()), nil
	}
	log.Printf("launched %s", id)
	return encodeLaunchResponse(true, ""), nil
}

func getDesktopIcon(id string) string {
	for _, dir := range appDirs {
		data, err := os.ReadFile(filepath.Join(dir, id+".desktop"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Icon=") {
				return strings.TrimSpace(line[5:])
			}
		}
	}
	return id
}

func findIconFile(iconName string, size int) []byte {
	home := os.Getenv("HOME")
	user := os.Getenv("USER")

	sizes := []int{size, 64, 128, 48, 32, 256}
	seen := map[string]bool{}
	for _, s := range sizes {
		paths := []string{
			fmt.Sprintf("/run/current-system/sw/share/icons/hicolor/%dx%d/apps/%s.png", s, s, iconName),
			fmt.Sprintf("/usr/share/icons/hicolor/%dx%d/apps/%s.png", s, s, iconName),
			fmt.Sprintf("/run/current-system/sw/share/pixmaps/%s.png", iconName),
			fmt.Sprintf("/usr/share/pixmaps/%s.png", iconName),
		}
		if user != "" {
			paths = append(paths,
				fmt.Sprintf("/etc/profiles/per-user/%s/share/icons/hicolor/%dx%d/apps/%s.png", user, s, s, iconName),
				fmt.Sprintf("/etc/profiles/per-user/%s/share/pixmaps/%s.png", user, iconName))
		}
		if home != "" {
			paths = append(paths,
				fmt.Sprintf("%s/.local/share/icons/hicolor/%dx%d/apps/%s.png", home, s, s, iconName),
				fmt.Sprintf("%s/.local/share/pixmaps/%s.png", home, iconName))
		}
		if s == sizes[0] {
			paths = append(paths,
				fmt.Sprintf("/run/current-system/sw/share/icons/hicolor/scalable/apps/%s.svg", iconName),
				fmt.Sprintf("/usr/share/icons/hicolor/scalable/apps/%s.svg", iconName))
			if user != "" {
				paths = append(paths,
					fmt.Sprintf("/etc/profiles/per-user/%s/share/icons/hicolor/scalable/apps/%s.svg", user, iconName))
			}
			if home != "" {
				paths = append(paths,
					fmt.Sprintf("%s/.local/share/icons/hicolor/scalable/apps/%s.svg", home, iconName))
			}
		}
		for _, path := range paths {
			if seen[path] {
				continue
			}
			seen[path] = true
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if strings.HasSuffix(path, ".svg") {
				cmd := exec.Command("rsvg-convert", "--width", fmt.Sprintf("%d", size),
					"--height", fmt.Sprintf("%d", size), "-f", "png", path)
				pngData, err := cmd.Output()
				if err == nil && len(pngData) > 0 {
					return pngData
				}
				return data
			}
			return data
		}
	}
	return nil
}

func (c *vmCicerone) GetContainerAppIcon(req []byte) ([]byte, *dbus.Error) {
	fields := decodeStringFields(req)
	intFields := decodeUint64Fields(req)
	id := fields[3] // desktop_file_id
	reqSize := int(intFields[4])
	if reqSize < 16 || reqSize > 512 {
		reqSize = 48
	}
	log.Printf("GetContainerAppIcon: id=%q iconName=%q size=%d", id, getDesktopIcon(id), reqSize)
	if id == "" {
		return []byte{}, nil
	}
	iconName := getDesktopIcon(id)
	iconData := findIconFile(iconName, reqSize)
	icon := appendString(nil, 1, id)
	if iconData != nil {
		format := uint64(0)
		if len(iconData) > 4 && iconData[0] == '<' {
			format = 1
		}
		log.Printf("  found icon %q = %d bytes format=%d", iconName, len(iconData), format)
		icon = appendBytes(icon, 2, iconData)
		icon = appendUint64(icon, 3, format)
	} else {
		log.Printf("  NO icon found for %q (name=%q)", id, iconName)
	}
	b := appendBytes(nil, 1, icon) // repeated icons field
	return b, nil
}

func (c *vmCicerone) StartLxd(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("StartLxd", decodeStringFields(req))
	log.Printf("StartLxd called")
	return appendUint64(nil, 1, 2), nil // status=ALREADY_RUNNING (2)
}

func (c *vmCicerone) StartLxdContainer(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("StartLxdContainer", decodeStringFields(req))
	log.Printf("StartLxdContainer called")
	return appendUint64(nil, 1, 2), nil
}

func (c *vmCicerone) CreateLxdContainer(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("CreateLxdContainer", decodeStringFields(req))
	log.Printf("CreateLxdContainer called")
	return appendUint64(nil, 1, 2), nil // status=EXISTS (2)
}

func (c *vmCicerone) SetUpLxdContainerUser(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("SetUpLxdContainerUser", decodeStringFields(req))
	log.Printf("SetUpLxdContainerUser called")
	return appendUint64(nil, 1, 1), nil // status=SUCCESS (1)
}

func (c *vmCicerone) GetLxdContainerUsername(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("GetLxdContainerUsername", decodeStringFields(req))
	log.Printf("GetLxdContainerUsername called")
	b := appendUint64(nil, 1, 1) // status=SUCCEEDED (1)
	b = appendString(b, 2, containerUser)
	return b, nil
}

func (c *vmCicerone) GetGarconPort(req []byte) ([]byte, *dbus.Error) {
	log.Printf("GetGarconPort called")
	return appendUint64(nil, 1, 8989), nil // port = 8989
}

func (c *vmCicerone) GetGarconSessionInfo(req []byte) ([]byte, *dbus.Error) {
	learnOwnerID("GetGarconSessionInfo", decodeStringFields(req))
	log.Printf("GetGarconSessionInfo called")
	b := appendUint64(nil, 1, 1) // status=SUCCEEDED (1; FAILED=3)
	b = appendString(b, 3, containerUser)
	b = appendString(b, 4, "/home/user")
	b = appendUint64(b, 5, 0) // sftp_vsock_port
	return b, nil
}

type dlcService struct {
	conn *dbus.Conn
}

func isInstalledDlc(id string) bool {
	return id == "termina-dlc" || id == "cros-termina"
}

func encodeDlcState(id string) []byte {
	stateValue := uint64(0) // NOT_INSTALLED
	lastError := dlcErrorNoImageFound
	rootPath := ""
	if isInstalledDlc(id) {
		stateValue = 2 // INSTALLED
		lastError = dlcErrorNone
		rootPath = "/var/lib/dlc/" + id + "/package/root"
	}
	state := appendUint64(nil, 1, stateValue)
	state = appendString(state, 2, id)
	if rootPath != "" {
		state = appendString(state, 3, rootPath)
	}
	state = appendString(state, 5, lastError)
	return state
}

func (d *dlcService) Install(req []byte) *dbus.Error {
	id := decodeStringFields(req)[1]
	if id == "" {
		id = "termina-dlc"
	}
	log.Printf("DlcService.Install called id=%q", id)
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := d.conn.Emit(dlcServiceObj, dlcServiceIface+".DlcStateChanged", encodeDlcState(id)); err != nil {
			log.Printf("DlcService.DlcStateChanged emit failed: %v", err)
			return
		}
		log.Printf("DlcService emitted DlcStateChanged id=%q", id)
	}()
	return nil
}

func (d *dlcService) GetDlcState(req []byte) ([]byte, *dbus.Error) {
	id := decodeStringFields(req)[1]
	if id == "" {
		id = "termina-dlc"
	}
	log.Printf("DlcService.GetDlcState called id=%q", id)
	return encodeDlcState(id), nil
}

func (d *dlcService) GetExistingDlcs(req []byte) ([]byte, *dbus.Error) {
	log.Printf("DlcService.GetExistingDlcs called")
	return appendBytes(nil, 1, encodeDlcState("termina-dlc")), nil
}

func (d *dlcService) InstallWithOmaha(req []byte) *dbus.Error {
	log.Printf("DlcService.InstallWithOmaha called")
	return nil
}

type debugdStub struct{}

func (d *debugdStub) SetSchedulerConfigurationV2(configName string, lockPolicy bool) (bool, uint32, *dbus.Error) {
	log.Printf("debugd.SetSchedulerConfigurationV2 configName=%q lockPolicy=%v", configName, lockPolicy)
	return true, 0, nil
}

type imageLoader struct{}

func (i *imageLoader) RegisterComponent(name, version, path string) (bool, *dbus.Error) {
	log.Printf("ImageLoader.RegisterComponent name=%q version=%q path=%q", name, version, path)
	return true, nil
}

func (i *imageLoader) GetComponentVersion(name string) (string, *dbus.Error) {
	log.Printf("ImageLoader.GetComponentVersion name=%q", name)
	return "99999.0.0", nil
}

func (i *imageLoader) LoadComponent(name string) (string, *dbus.Error) {
	log.Printf("ImageLoader.LoadComponent name=%q", name)
	return terminaMount, nil
}

func (i *imageLoader) LoadComponentAtPath(name, path string) (string, *dbus.Error) {
	log.Printf("ImageLoader.LoadComponentAtPath name=%q path=%q", name, path)
	return terminaMount, nil
}

func (i *imageLoader) LoadDlcImage(id, pkg, slot string) (string, *dbus.Error) {
	log.Printf("ImageLoader.LoadDlcImage id=%q package=%q slot=%q", id, pkg, slot)
	return "/var/lib/dlc/" + id + "/" + pkg + "/root", nil
}

func (i *imageLoader) LoadDlc(req []byte) (string, *dbus.Error) {
	log.Printf("ImageLoader.LoadDlc request_len=%d", len(req))
	return "/var/lib/dlc/termina-dlc/package/root", nil
}

func (i *imageLoader) RemoveComponent(name string) (bool, *dbus.Error) {
	log.Printf("ImageLoader.RemoveComponent name=%q", name)
	return true, nil
}

func (i *imageLoader) UnmountComponent(name string) (bool, *dbus.Error) {
	log.Printf("ImageLoader.UnmountComponent name=%q", name)
	return true, nil
}

func (i *imageLoader) UnloadDlcImage(id, pkg string) (bool, *dbus.Error) {
	log.Printf("ImageLoader.UnloadDlcImage id=%q package=%q", id, pkg)
	return true, nil
}

func main() {
	log.SetPrefix("[crostini-bridge] ")

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatalf("connect to system bus: %v", err)
	}
	defer conn.Close()

	reply, err := conn.RequestName(conciergeBus, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", conciergeBus, err, reply)
	}
	reply, err = conn.RequestName(ciceronebus, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", ciceronebus, err, reply)
	}
	reply, err = conn.RequestName(dlcServiceBus, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", dlcServiceBus, err, reply)
	}
	reply, err = conn.RequestName(debugdBus, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", debugdBus, err, reply)
	}
	reply, err = conn.RequestName(imageLoaderBus, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", imageLoaderBus, err, reply)
	}
	log.Printf("registered %s, %s, %s, %s, %s", conciergeBus, ciceronebus, dlcServiceBus, debugdBus, imageLoaderBus)

	log.Printf("appDirs = %v", appDirs)
	cicerone := &vmCicerone{}
	conn.Export(cicerone, ciceroneObj, ciceroneIface)
	conn.Export(&vmConcierge{conn: conn}, conciergeObj, conciergeIface)
	conn.Export(&dlcService{conn: conn}, dlcServiceObj, dlcServiceIface)
	conn.Export(&debugdStub{}, debugdObj, debugdIface)
	conn.Export(&imageLoader{}, imageLoaderObj, imageLoaderIface)

	go func() {
		time.Sleep(15 * time.Second)
		apps := scanApps()
		cicerone.mu.Lock()
		cicerone.apps = apps
		cicerone.mu.Unlock()
		log.Printf("scanned %d apps for launching", len(apps))
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP)
	go func() {
		for range sig {
			log.Printf("SIGHUP received, re-scanning apps")
			apps := scanApps()
			cicerone.mu.Lock()
			cicerone.apps = apps
			cicerone.mu.Unlock()
			log.Printf("re-scanned %d apps", len(apps))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
