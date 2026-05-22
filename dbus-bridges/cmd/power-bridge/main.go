// power-bridge maps Ash power and battery calls onto UPower and systemd.
package main

import (
	"encoding/binary"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	dbus "github.com/godbus/dbus/v5"
)

const (
	powerService = "org.chromium.PowerManager"
	powerPath    = dbus.ObjectPath("/org/chromium/PowerManager")
	powerIface   = "org.chromium.PowerManager"
)

const (
	battStateFull        = 0
	battStateCharging    = 1
	battStateDischarging = 2
	battStateNotPresent  = 3
)

const (
	extPowerAC           = 0
	extPowerUSB          = 1
	extPowerDisconnected = 2
)

type batteryState struct {
	percent     float64
	state       uint32
	timeToEmpty int64
	timeToFull  int64
}

type powerSupplyProps struct {
	percent           float64
	state             uint32
	externalPower     uint32
	timeToEmptySec    int64
	timeToFullSec     int64
	isCalcBatteryTime bool
	batteryPresent    bool
	chargeLimited     bool
}

func encodePowerSupplyProps(p *powerSupplyProps) []byte {
	var buf []byte

	if p.timeToEmptySec != 0 {
		buf = appendVarint(buf, 0x28, uint64(p.timeToEmptySec))
	}
	if p.timeToFullSec != 0 {
		buf = appendVarint(buf, 0x30, uint64(p.timeToFullSec))
	}
	if p.batteryPresent {
		buf = append(buf, 0x39)
		var fbits [8]byte
		binary.LittleEndian.PutUint64(fbits[:], math.Float64bits(p.percent))
		buf = append(buf, fbits[:]...)
	}
	if p.batteryPresent {
		if p.isCalcBatteryTime {
			buf = appendVarint(buf, 0x60, 1)
		} else {
			buf = appendVarint(buf, 0x60, 0)
		}
	}
	buf = appendVarint(buf, 0x70, uint64(p.externalPower))
	buf = appendVarint(buf, 0x78, uint64(p.state))

	return buf
}

func appendVarint(buf []byte, tag byte, v uint64) []byte {
	buf = append(buf, tag)
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	buf = append(buf, byte(v))
	return buf
}

type powerManager struct {
	conn             *dbus.Conn
	mu               sync.RWMutex
	batt             batteryState
	screenBrightness float64
}

func (p *powerManager) GetBatteryState() (float64, uint32, int64, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.batt.percent, p.batt.state, p.batt.timeToEmpty, nil
}

func (p *powerManager) GetScreenBrightnessPercent() (float64, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.screenBrightness, nil
}

func (p *powerManager) GetKeyboardBrightnessPercent() (float64, *dbus.Error) {
	return 0, nil
}

func (p *powerManager) SetScreenBrightness(req []byte) *dbus.Error {
	percent, ok := parseSetBacklightBrightnessRequest(req)
	if !ok {
		log.Printf("SetScreenBrightness: failed to parse request %x", req)
		return nil
	}
	p.setScreenBrightness(percent)
	return nil
}

func (p *powerManager) SetScreenBrightnessPercent(percent float64, gradual bool) *dbus.Error {
	p.setScreenBrightness(percent)
	return nil
}

func (p *powerManager) IncreaseScreenBrightness() *dbus.Error {
	p.mu.RLock()
	next := p.screenBrightness + 10
	p.mu.RUnlock()
	p.setScreenBrightness(next)
	return nil
}

func (p *powerManager) DecreaseScreenBrightness(allowOff bool) *dbus.Error {
	p.mu.RLock()
	next := p.screenBrightness - 10
	p.mu.RUnlock()
	if !allowOff && next < 1 {
		next = 1
	}
	p.setScreenBrightness(next)
	return nil
}

func (p *powerManager) SetPolicy(req []byte) *dbus.Error {
	return nil
}

func (p *powerManager) GetPowerSupplyProperties() ([]byte, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return encodePowerSupplyProps(powerSupplyPropsFromBattery(p.batt)), nil
}

func (p *powerManager) GetThermalState() ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (p *powerManager) RegisterSuspendDelay(req []byte) ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (p *powerManager) RegisterDarkSuspendDelay(req []byte) ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (p *powerManager) UnregisterSuspendDelay(req []byte) *dbus.Error {
	return nil
}

func (p *powerManager) UnregisterDarkSuspendDelay(req []byte) *dbus.Error {
	return nil
}

func (p *powerManager) HandleSuspendReadiness(req []byte) *dbus.Error {
	return nil
}

func (p *powerManager) HandleDarkSuspendReadiness(req []byte) *dbus.Error {
	return nil
}

func (p *powerManager) GetBatterySaverModeState() ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (p *powerManager) GetSwitchStates() ([]byte, *dbus.Error) {
	return []byte{0x10, 0x02}, nil
}

func (p *powerManager) HasAmbientLightSensor() (bool, *dbus.Error) {
	return false, nil
}

func (p *powerManager) HasKeyboardBacklight() (bool, *dbus.Error) {
	return false, nil
}

func (p *powerManager) GetBacklightsForcedOff() (bool, *dbus.Error) {
	return false, nil
}

func (p *powerManager) GetChargeHistory() ([]byte, *dbus.Error) {
	return []byte{}, nil
}

func (p *powerManager) RefreshAllPeripheralBattery() *dbus.Error {
	return nil
}

func (p *powerManager) RequestSuspend(externalWakeupCount uint64) *dbus.Error {
	log.Println("RequestSuspend")
	go systemctlAction("suspend")
	return nil
}

func (p *powerManager) RequestRestart(reason int32, description string) *dbus.Error {
	log.Printf("RequestRestart reason=%d", reason)
	go systemctlAction("reboot")
	return nil
}

func (p *powerManager) RequestShutdown(reason int32, description string) *dbus.Error {
	log.Printf("RequestShutdown reason=%d", reason)
	go systemctlAction("poweroff")
	return nil
}

func (p *powerManager) HandleUserActivity(activityType int32) *dbus.Error {
	return nil
}

func (p *powerManager) setScreenBrightness(percent float64) {
	percent = clampPercent(percent)
	if err := applyScreenBrightness(percent); err != nil {
		log.Printf("set screen brightness %.1f%%: %v", percent, err)
	}

	p.mu.Lock()
	p.screenBrightness = percent
	p.mu.Unlock()

	p.conn.Emit(powerPath, powerIface+".ScreenBrightnessChanged", encodeBacklightBrightnessChange(percent, 0))
	log.Printf("screen brightness %.1f%%", percent)
}

func systemctlAction(action string) {
	systemctl := "systemctl"
	if _, err := os.Stat("/run/current-system/sw/bin/systemctl"); err == nil {
		systemctl = "/run/current-system/sw/bin/systemctl"
	}
	if err := exec.Command(systemctl, action).Run(); err != nil {
		log.Printf("systemctl %s: %v", action, err)
	}
}

func parseSetBacklightBrightnessRequest(req []byte) (float64, bool) {
	for i := 0; i < len(req); {
		tag := req[i]
		i++
		field := tag >> 3
		wire := tag & 7
		if field == 1 && wire == 1 {
			if i+8 > len(req) {
				return 0, false
			}
			return math.Float64frombits(binary.LittleEndian.Uint64(req[i : i+8])), true
		}

		switch wire {
		case 0:
			for i < len(req) {
				b := req[i]
				i++
				if b < 0x80 {
					break
				}
			}
		case 1:
			i += 8
		case 2:
			n, next, ok := readProtoVarint(req, i)
			if !ok {
				return 0, false
			}
			i = next + int(n)
		case 5:
			i += 4
		default:
			return 0, false
		}
		if i > len(req) {
			return 0, false
		}
	}
	return 0, false
}

func readProtoVarint(buf []byte, off int) (uint64, int, bool) {
	var v uint64
	var shift uint
	for i := off; i < len(buf); i++ {
		b := buf[i]
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, i + 1, true
		}
		shift += 7
		if shift >= 64 {
			return 0, off, false
		}
	}
	return 0, off, false
}

func encodeBacklightBrightnessChange(percent float64, cause uint64) []byte {
	var buf []byte
	buf = append(buf, 0x09)
	var fbits [8]byte
	binary.LittleEndian.PutUint64(fbits[:], math.Float64bits(clampPercent(percent)))
	buf = append(buf, fbits[:]...)
	buf = appendVarint(buf, 0x10, cause)
	return buf
}

func clampPercent(percent float64) float64 {
	if math.IsNaN(percent) {
		return 100
	}
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func initialScreenBrightness() float64 {
	if pct, ok := readBacklightPercent(); ok {
		return pct
	}
	return 100
}

func applyScreenBrightness(percent float64) error {
	if err := writeBacklightPercent(percent); err == nil {
		return nil
	}
	return applyXrandrBrightness(percent)
}

func readBacklightPercent() (float64, bool) {
	devs, err := filepath.Glob("/sys/class/backlight/*")
	if err != nil || len(devs) == 0 {
		return 0, false
	}
	cur, err := readFloatFile(filepath.Join(devs[0], "brightness"))
	if err != nil {
		return 0, false
	}
	max, err := readFloatFile(filepath.Join(devs[0], "max_brightness"))
	if err != nil || max <= 0 {
		return 0, false
	}
	return clampPercent(cur * 100 / max), true
}

func writeBacklightPercent(percent float64) error {
	devs, err := filepath.Glob("/sys/class/backlight/*")
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		return os.ErrNotExist
	}
	max, err := readFloatFile(filepath.Join(devs[0], "max_brightness"))
	if err != nil {
		return err
	}
	level := int(math.Round(clampPercent(percent) * max / 100))
	if percent > 0 && level < 1 {
		level = 1
	}
	return os.WriteFile(filepath.Join(devs[0], "brightness"), []byte(strconv.Itoa(level)+"\n"), 0)
}

func readFloatFile(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

func applyXrandrBrightness(percent float64) error {
	xrandr := "xrandr"
	if _, err := os.Stat("/run/current-system/sw/bin/xrandr"); err == nil {
		xrandr = "/run/current-system/sw/bin/xrandr"
	}
	outputs, err := connectedXrandrOutputs(xrandr)
	if err != nil {
		return err
	}
	if len(outputs) == 0 {
		return os.ErrNotExist
	}
	ratio := clampPercent(percent) / 100
	if ratio < 0.01 {
		ratio = 0.01
	}
	value := strconv.FormatFloat(ratio, 'f', 3, 64)
	for _, output := range outputs {
		cmd := exec.Command(xrandr, "--output", output, "--brightness", value)
		cmd.Env = x11Env()
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	return nil
}

func connectedXrandrOutputs(xrandr string) ([]string, error) {
	cmd := exec.Command(xrandr, "--query")
	cmd.Env = x11Env()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var outputs []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "connected" {
			outputs = append(outputs, fields[0])
		}
	}
	return outputs, nil
}

func x11Env() []string {
	env := os.Environ()
	if os.Getenv("DISPLAY") == "" {
		env = append(env, "DISPLAY=:0")
	}
	if os.Getenv("XAUTHORITY") == "" {
		env = append(env, "XAUTHORITY=/tmp/ash-xauth")
	}
	return env
}

func getProp[T any](obj dbus.BusObject, iface, prop string) T {
	var v dbus.Variant
	obj.Call("org.freedesktop.DBus.Properties.Get", 0, iface, prop).Store(&v)
	val, _ := v.Value().(T)
	return val
}

func upowerBatteryState(conn *dbus.Conn) batteryState {
	upower := conn.Object("org.freedesktop.UPower", "/org/freedesktop/UPower")

	var devices []dbus.ObjectPath
	if err := upower.Call("org.freedesktop.UPower.EnumerateDevices", 0).Store(&devices); err != nil {
		log.Printf("UPower.EnumerateDevices: %v", err)
		return batteryState{percent: 100, state: battStateNotPresent}
	}

	for _, path := range devices {
		dev := conn.Object("org.freedesktop.UPower", path)
		if getProp[uint32](dev, "org.freedesktop.UPower.Device", "Type") != 2 {
			continue // not a battery
		}
		if !getProp[bool](dev, "org.freedesktop.UPower.Device", "IsPresent") {
			continue
		}

		pct := getProp[float64](dev, "org.freedesktop.UPower.Device", "Percentage")
		upState := getProp[uint32](dev, "org.freedesktop.UPower.Device", "State")
		tte := getProp[int64](dev, "org.freedesktop.UPower.Device", "TimeToEmpty")
		ttf := getProp[int64](dev, "org.freedesktop.UPower.Device", "TimeToFull")

		log.Printf("battery: %.0f%% state=%d tte=%ds ttf=%ds", pct, upState, tte, ttf)
		return batteryState{percent: pct, state: upowerToPowerd(upState), timeToEmpty: tte, timeToFull: ttf}
	}

	log.Printf("no battery found (desktop/VM)")
	return batteryState{percent: 100, state: battStateNotPresent}
}

func upowerToPowerd(s uint32) uint32 {
	switch s {
	case 1, 5:
		return battStateCharging
	case 2, 3, 6:
		return battStateDischarging
	case 4:
		return battStateFull
	default:
		return battStateNotPresent
	}
}

func powerSupplyPropsFromBattery(b batteryState) *powerSupplyProps {
	extPower := extPowerAC
	if b.state == battStateDischarging {
		extPower = extPowerDisconnected
	}

	battPresent := b.state != battStateNotPresent
	isCalculating := false
	switch b.state {
	case battStateCharging:
		isCalculating = b.timeToFull <= 0
	case battStateDischarging:
		isCalculating = b.timeToEmpty <= 0
	}

	return &powerSupplyProps{
		percent:           b.percent,
		state:             b.state,
		externalPower:     uint32(extPower),
		timeToEmptySec:    b.timeToEmpty,
		timeToFullSec:     b.timeToFull,
		isCalcBatteryTime: battPresent && isCalculating,
		batteryPresent:    battPresent,
	}
}

func main() {
	log.SetPrefix("[power-bridge] ")

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatalf("connect to system bus: %v", err)
	}
	defer conn.Close()

	reply, err := conn.RequestName(powerService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", powerService, err, reply)
	}

	pm := &powerManager{
		conn:             conn,
		batt:             upowerBatteryState(conn),
		screenBrightness: initialScreenBrightness(),
	}
	conn.Export(pm, powerPath, powerIface)

	log.Printf("registered %s", powerService)
	conn.Emit(powerPath, powerIface+".ScreenBrightnessChanged", encodeBacklightBrightnessChange(pm.screenBrightness, 8))
	pm.emitPowerSupplyPoll()

	go func() {
		for range time.Tick(30 * time.Second) {
			pm.emitPowerSupplyPoll()
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}

func (p *powerManager) emitPowerSupplyPoll() {
	b := upowerBatteryState(p.conn)
	p.mu.Lock()
	p.batt = b
	p.mu.Unlock()

	payload := encodePowerSupplyProps(powerSupplyPropsFromBattery(b))
	if err := p.conn.Emit(powerPath, powerIface+".PowerSupplyPoll", payload); err != nil {
		log.Printf("emit PowerSupplyPoll: %v", err)
	}
}
