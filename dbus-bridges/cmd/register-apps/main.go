// register-apps: writes Linux .desktop files to crostini.registry in Chrome's
// Preferences so they appear in the Ash app launcher.
// Usage: register-apps /path/to/Preferences
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
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
}

type desktopApp struct {
	id      string
	name    string
	exec    string
	wmClass string
}

func parseDesktop(path, id string) (desktopApp, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return desktopApp{}, false
	}
	var name, execLine, appType, wmClass string
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
		case "StartupWMClass":
			wmClass = strings.TrimSpace(v)
		}
	}
	if appType != "Application" || name == "" {
		return desktopApp{}, false
	}
	return desktopApp{id: id, name: name, exec: execLine, wmClass: wmClass}, true
}

func scanApps() []desktopApp {
	seen := map[string]bool{}
	var apps []desktopApp
	for _, dir := range appDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
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
	return apps
}

func generateAppID(vmName, containerName, desktopFileID string) string {
	input := "crostini:" + vmName + "/" + containerName + "/" + desktopFileID
	hash := sha256.Sum256([]byte(input))
	id := make([]byte, 32)
	for i, b := range hash[:16] {
		id[i*2] = 'a' + (b >> 4)
		id[i*2+1] = 'a' + (b & 0xf)
	}
	return string(id)
}

func windowsEpochMicros(t time.Time) string {
	var windowsEpoch = time.Date(1601, 1, 1, 0, 0, 0, 0, time.UTC)
	delta := t.Sub(windowsEpoch)
	return fmt.Sprintf("%d", delta.Microseconds())
}

type appEntry map[string]any

func buildAppEntry(app desktopApp, vmName, containerName string, vmType int) appEntry {
	now := windowsEpochMicros(time.Now())
	return appEntry{
		"desktop_file_id":      app.id,
		"vm_type":              vmType,
		"vm_name":              vmName,
		"container_name":       containerName,
		"name":                 map[string]string{"": app.name},
		"exec":                 app.exec,
		"executable_file_name": "",
		"extensions":           []string{},
		"mime_types":           []string{},
		"keywords":             map[string][]string{"": {}},
		"no_display":           false,
		"terminal":             false,
		"startup_wm_class":     app.wmClass,
		"startup_notify":       false,
		"package_id":           "",
		"install_time":         now,
		"last_launch_time":     "",
	}
}

func main() {
	log.SetPrefix("[register-apps] ")

	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s /path/to/Preferences", os.Args[0])
	}
	prefsPath := os.Args[1]

	apps := scanApps()
	log.Printf("found %d apps", len(apps))

	vmName := "termina"
	containerName := "penguin"
	vmType := 0 // TERMINA

	prefs := map[string]any{}

	if data, err := os.ReadFile(prefsPath); err == nil {
		if err := json.Unmarshal(data, &prefs); err != nil {
			log.Printf("warning: failed to parse existing prefs: %v", err)
		}
	}

	crostini, ok := prefs["crostini"].(map[string]any)
	if !ok {
		crostini = map[string]any{}
		prefs["crostini"] = crostini
	}
	crostini["enabled"] = true

	registry := map[string]appEntry{}
	if existing, ok := crostini["registry"]; ok {
		if existingMap, ok := existing.(map[string]any); ok {
			for k, v := range existingMap {
				if entry, ok := v.(map[string]any); ok {
					e := appEntry{}
					for ek, ev := range entry {
						e[ek] = ev
					}
					registry[k] = e
				}
			}
		}
	}

	for _, app := range apps {
		id := generateAppID(vmName, containerName, app.id)
		if _, exists := registry[id]; !exists {
			registry[id] = buildAppEntry(app, vmName, containerName, vmType)
		}
	}

	crostini["registry"] = registry

	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		log.Fatalf("marshal prefs: %v", err)
	}

	if err := os.WriteFile(prefsPath, data, 0644); err != nil {
		log.Fatalf("write prefs: %v", err)
	}

	log.Printf("wrote %d apps to %s", len(apps), prefsPath)
}
