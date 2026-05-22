// cras-bridge maps Ash audio calls onto PipeWire/PulseAudio.
package main

import (
	"log"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	dbus "github.com/godbus/dbus/v5"
)

const (
	crasService = "org.chromium.cras"
	crasPath    = dbus.ObjectPath("/org/chromium/cras")
	crasIface   = "org.chromium.cras.Control"
)

type audioState struct {
	volume      int32
	outputMuted bool
	inputGain   int32
	inputMuted  bool
}

type crasControl struct {
	conn *dbus.Conn
	mu   sync.RWMutex
	st   audioState
}

func (c *crasControl) GetVolumeState() (int32, bool, int32, bool, bool, *dbus.Error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.st.volume, false, c.st.inputGain, c.st.inputMuted, c.st.outputMuted, nil
}

func (c *crasControl) SetActiveOutputNode(nodeID uint64) *dbus.Error {
	log.Printf("SetActiveOutputNode: 0x%x", nodeID)
	return nil
}

func (c *crasControl) SetActiveInputNode(nodeID uint64) *dbus.Error {
	log.Printf("SetActiveInputNode: 0x%x", nodeID)
	return nil
}

func (c *crasControl) SetOutputNodeVolume(nodeID uint64, volume int32) *dbus.Error {
	log.Printf("SetOutputNodeVolume: node=0x%x volume=%d", nodeID, volume)
	if volume < 0 {
		volume = 0
	}
	if volume > 100 {
		volume = 100
	}
	c.mu.Lock()
	c.st.volume = volume
	c.mu.Unlock()
	pactl("set-sink-volume", "@DEFAULT_SINK@", strconv.Itoa(int(volume))+"%")
	return nil
}

func (c *crasControl) SetOutputVolume(volume int32) *dbus.Error {
	if volume < 0 {
		volume = 0
	}
	if volume > 100 {
		volume = 100
	}
	log.Printf("SetOutputVolume: %d", volume)
	c.mu.Lock()
	c.st.volume = volume
	c.mu.Unlock()
	pactl("set-sink-volume", "@DEFAULT_SINK@", strconv.Itoa(int(volume))+"%")
	return nil
}

func (c *crasControl) SetOutputUserMute(muted bool) *dbus.Error {
	log.Printf("SetOutputUserMute: %v", muted)
	c.mu.Lock()
	c.st.outputMuted = muted
	c.mu.Unlock()
	v := "0"
	if muted {
		v = "1"
	}
	pactl("set-sink-mute", "@DEFAULT_SINK@", v)
	return nil
}

func (c *crasControl) SetInputMute(muted bool) *dbus.Error {
	log.Printf("SetInputMute: %v", muted)
	c.mu.Lock()
	c.st.inputMuted = muted
	c.mu.Unlock()
	return nil
}

func (c *crasControl) SetInputNodeGain(nodeID uint64, gain int32) *dbus.Error {
	log.Printf("SetInputNodeGain: node=0x%x gain=%d", nodeID, gain)
	c.mu.Lock()
	c.st.inputGain = gain
	c.mu.Unlock()
	return nil
}

func (c *crasControl) SwapLeftRight(nodeID uint64, swap bool) *dbus.Error {
	return nil
}

func (c *crasControl) GetDefaultOutputBufferSize() (int32, *dbus.Error) {
	return 512, nil
}

func (c *crasControl) GetNumberOfActiveOutputStreams() (int32, *dbus.Error) {
	out, err := exec.Command("pactl", "list", "sink-inputs", "short").Output()
	if err != nil {
		return 0, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0, nil
	}
	return int32(len(lines)), nil
}

func (c *crasControl) GetNumberOfArcStreams() (int32, *dbus.Error) {
	return 0, nil
}

func (c *crasControl) GetNumberOfInputStreamsWithPermission() ([]map[string]dbus.Variant, *dbus.Error) {
	return nil, nil
}

func (c *crasControl) GetNumberOfNonChromeOutputStreams() (int32, *dbus.Error) {
	return 0, nil
}

func (c *crasControl) GetNumStreamIgnoreUiGains() (int32, *dbus.Error) {
	return 0, nil
}

func (c *crasControl) GetSidetoneSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) GetSystemAecGroupId() (int32, *dbus.Error) {
	return -1, nil
}

func (c *crasControl) GetSystemAecSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) GetSystemAgcSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) GetSystemNsSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) IsSpatialAudioSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) IsHfpMicSrSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) IsNoiseCancellationSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) GetAudioEffectDlcs() (string, *dbus.Error) {
	return "", nil
}

func (c *crasControl) IsStyleTransferSupported() (bool, *dbus.Error) {
	return false, nil
}

func (c *crasControl) GetVoiceIsolationUIAppearance() (uint32, uint32, bool, *dbus.Error) {
	return 0, 0, false, nil
}

func (c *crasControl) SetSpatialAudio(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetFlossEnabled(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetFixA2dpPacketSize(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetForceRespectUiGains(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetEwmaPowerReportEnabled(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetKrispNoiseCancellationEnabled(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetSidetoneEnabled(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetSpeakOnMuteDetection(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetVoiceIsolationUIEnabled(enabled bool) *dbus.Error {
	return nil
}

func (c *crasControl) SetVoiceIsolationUIPreferredEffect(effect uint32) *dbus.Error {
	return nil
}

func pactl(args ...string) {
	if err := exec.Command("pactl", args...).Run(); err != nil {
		log.Printf("pactl %v: %v", args, err)
	}
}

func pactlVolume() (vol int32, muted bool, ok bool) {
	out, err := exec.Command("pactl", "get-sink-volume", "@DEFAULT_SINK@").Output()
	if err != nil {
		return 0, false, false
	}
	// "Volume: front-left: 49152 /  75% / -8.00 dB, ..."
	s := string(out)
	parts := strings.SplitN(s, "%", 2)
	if len(parts) < 2 {
		return 0, false, false
	}
	fields := strings.Fields(parts[0])
	if len(fields) == 0 {
		return 0, false, false
	}
	n, err := strconv.ParseInt(fields[len(fields)-1], 10, 32)
	if err != nil {
		return 0, false, false
	}

	muteOut, err := exec.Command("pactl", "get-sink-mute", "@DEFAULT_SINK@").Output()
	if err == nil {
		muted = strings.Contains(string(muteOut), "yes")
	}
	return int32(n), muted, true
}

func makeNode(id uint64, isInput bool, nodeType, name, deviceName string, active bool, nodeVolume uint64) map[string]dbus.Variant {
	channels := uint32(2)
	if isInput {
		channels = 1
	}
	return map[string]dbus.Variant{
		"IsInput":              dbus.MakeVariant(isInput),
		"Id":                   dbus.MakeVariant(id),
		"StableDeviceId":       dbus.MakeVariant(id >> 32),
		"StableDeviceIdNew":    dbus.MakeVariant(id),
		"Type":                 dbus.MakeVariant(nodeType),
		"Name":                 dbus.MakeVariant(name),
		"DeviceName":           dbus.MakeVariant(deviceName),
		"Active":               dbus.MakeVariant(active),
		"PluggedTime":          dbus.MakeVariant(uint64(0)),
		"NodeVolume":           dbus.MakeVariant(nodeVolume),
		"InputNodeGain":        dbus.MakeVariant(uint64(50)),
		"MaxSupportedChannels": dbus.MakeVariant(channels),
		"AudioEffect":          dbus.MakeVariant(uint32(0)),
		"NumberOfVolumeSteps":  dbus.MakeVariant(int32(25)),
	}
}

func defaultNodes() []map[string]dbus.Variant {
	return []map[string]dbus.Variant{
		makeNode(0x0001_0000_0001, false, "INTERNAL_SPEAKER", "Speaker", "Internal Speaker", true, 100),
		makeNode(0x0002_0000_0001, false, "HEADPHONE", "Headphone", "Headphone Jack", false, 100),
		makeNode(0x0001_0000_0002, true, "INTERNAL_MIC", "Internal Mic", "Internal Microphone", true, 50),
	}
}

func makeGetNodesHandler() any {
	nodes := defaultNodes()
	n := len(nodes)

	outTypes := make([]reflect.Type, n+1)
	mapType := reflect.TypeOf(map[string]dbus.Variant(nil))
	for i := range n {
		outTypes[i] = mapType
	}
	outTypes[n] = reflect.TypeOf((*dbus.Error)(nil))

	funcType := reflect.FuncOf(nil, outTypes, false)

	return reflect.MakeFunc(funcType, func(args []reflect.Value) []reflect.Value {
		log.Printf("GetNodes called (reflect), returning %d nodes", n)
		results := make([]reflect.Value, n+1)
		for i := range n {
			results[i] = reflect.ValueOf(nodes[i])
		}
		results[n] = reflect.Zero(reflect.TypeOf((*dbus.Error)(nil)))
		return results
	}).Interface()
}

func crasMethods(ctrl *crasControl) map[string]any {
	methods := make(map[string]any)
	val := reflect.ValueOf(ctrl)
	typ := val.Type()
	errType := reflect.TypeOf((*dbus.Error)(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if name == "GetNodes" {
			continue
		}
		method := val.Method(i)
		t := method.Type()
		if t.NumOut() == 0 || t.Out(t.NumOut()-1) != errType || typ.Method(i).PkgPath != "" {
			continue
		}
		methods[name] = method.Interface()
	}
	return methods
}

func main() {
	log.SetPrefix("[cras-bridge] ")

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatalf("connect to system bus: %v", err)
	}
	defer conn.Close()

	st := audioState{volume: 75, inputGain: 50}
	if vol, muted, ok := pactlVolume(); ok {
		st.volume = vol
		st.outputMuted = muted
		log.Printf("initial volume: %d%% muted=%v", vol, muted)
	} else {
		log.Println("PulseAudio/PipeWire not available — using default volume 75%")
	}

	ctrl := &crasControl{conn: conn, st: st}
	methods := crasMethods(ctrl)
	methods["GetNodes"] = makeGetNodesHandler()
	if err := conn.ExportMethodTable(methods, crasPath, crasIface); err != nil {
		log.Fatalf("export methods: %v", err)
	}

	reply, err := conn.RequestName(crasService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("request name %s: err=%v reply=%v", crasService, err, reply)
	}

	log.Printf("registered %s", crasService)

	go func() {
		lastVol := int32(-1)
		lastMute := false
		for range time.Tick(3 * time.Second) {
			vol, muted, ok := pactlVolume()
			if !ok {
				continue
			}
			volChanged := vol != lastVol
			muteChanged := muted != lastMute
			if !volChanged && !muteChanged {
				continue
			}
			ctrl.mu.Lock()
			ctrl.st.volume = vol
			ctrl.st.outputMuted = muted
			ctrl.mu.Unlock()
			lastVol, lastMute = vol, muted
			if volChanged {
				conn.Emit(crasPath, crasIface+".OutputVolumeChanged", vol)
			}
			if muteChanged {
				conn.Emit(crasPath, crasIface+".OutputMuteChanged", muted)
			}
		}
	}()

	go func() {
		time.Sleep(5 * time.Second)
		log.Printf("Emitting NodesChanged signal")
		conn.Emit(crasPath, crasIface+".NodesChanged")
		conn.Emit(crasPath, crasIface+".ActiveOutputNodeChanged", uint64(0x100000001))
		conn.Emit(crasPath, crasIface+".ActiveInputNodeChanged", uint64(0x100000002))
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
}
