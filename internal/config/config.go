// Package config is kkdesk's local configuration: the settings of the 设置
// page and the address book of 我的设备. Everything lives in one JSON file
// in the user's configuration directory (macOS ~/Library/Application
// Support/kkdesk, Windows %APPDATA%\kkdesk; KKDESK_CONFIG_DIR overrides
// it). There is no account: nothing leaves this machine.
//
// Writes are atomic (temporary file, then rename), the Store is safe for
// concurrent use, and a file that cannot be read is moved aside as
// kkdesk.json.bad so that the program still starts with the defaults.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// EnvDir overrides the configuration directory (tests, a second instance
// next to "tan dev").
const EnvDir = "KKDESK_CONFIG_DIR"

// FileName is the configuration file inside the directory.
const FileName = "kkdesk.json"

// DefaultPort is the default listening port of the host.
const DefaultPort = 21118

// Temporary password rotation (设置 › 安全 and the 连接 page).
const (
	RotateAfterSession = "session" // 每次连接后更换
	Rotate12h          = "12h"     // 每 12 小时更换
	Rotate24h          = "24h"     // 每 24 小时更换
	RotateNever        = "never"   // 不自动更换
)

// Theme of the windows.
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// Fixed password length limits.
const (
	FixedMin = 8
	FixedMax = 16
)

// Frame rates and bitrates offered by 设置 › 画质 (0 Mbps = automatic).
var (
	FrameRates = []int{30, 60, 90, 144}
	Bitrates   = []int{0, 30, 14, 8, 3}
)

// Settings are the 设置 page. The zero value is not valid; see Defaults.
type Settings struct {
	Rotate        string `json:"rotate"`
	FixedPassword string `json:"fixedPassword,omitempty"`
	Confirm       bool   `json:"confirm"`     // ask before accepting a connection
	AllowRemote   bool   `json:"allowRemote"` // listen at start
	FPS           int    `json:"fps"`
	BitrateMbps   int    `json:"bitrateMbps"` // 0 = automatic
	Port          int    `json:"port"`
	Theme         string `json:"theme"`
}

// Defaults are the settings of a new installation.
func Defaults() Settings {
	return Settings{Rotate: RotateAfterSession, Confirm: true, AllowRemote: true, FPS: 60, Port: DefaultPort, Theme: ThemeSystem}
}

// RotateEvery is the rotation period of a timed policy, 0 otherwise.
func RotateEvery(policy string) time.Duration {
	switch policy {
	case Rotate12h:
		return 12 * time.Hour
	case Rotate24h:
		return 24 * time.Hour
	}
	return 0
}

// ValidFixedPassword checks a new fixed password ("" clears it).
func ValidFixedPassword(pw string) error {
	if pw == "" {
		return nil
	}
	if n := len([]rune(pw)); n < FixedMin || n > FixedMax {
		return fmt.Errorf("固定密码需要 %d–%d 位", FixedMin, FixedMax)
	}
	return nil
}

// repair replaces invalid values by their defaults.
func (s *Settings) repair() {
	d := Defaults()
	switch s.Rotate {
	case RotateAfterSession, Rotate12h, Rotate24h, RotateNever:
	default:
		s.Rotate = d.Rotate
	}
	if ValidFixedPassword(s.FixedPassword) != nil {
		s.FixedPassword = ""
	}
	if !slices.Contains(FrameRates, s.FPS) {
		s.FPS = d.FPS
	}
	if !slices.Contains(Bitrates, s.BitrateMbps) {
		s.BitrateMbps = d.BitrateMbps
	}
	if s.Port < 1024 || s.Port > 65535 {
		s.Port = d.Port
	}
	switch s.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
	default:
		s.Theme = d.Theme
	}
}

// TempPassword is the current temporary password with the time it was
// made, kept so that a timed rotation policy survives restarts.
type TempPassword struct {
	Password string    `json:"password"`
	Since    time.Time `json:"since"`
}

// file is the JSON document.
type file struct {
	Version  int           `json:"version"`
	Settings Settings      `json:"settings"`
	Temp     *TempPassword `json:"temp,omitempty"`
	Groups   []string      `json:"groups"`
	Devices  []Device      `json:"devices"`
}

// ErrCorrupt is returned by Open when the file could not be parsed; it was
// moved aside (FileName + ".bad") and the defaults are used.
var ErrCorrupt = errors.New("config: file is corrupt")

// Store holds the configuration in memory and writes every change through.
type Store struct {
	path string

	mu     sync.Mutex
	f      file
	errMsg string // last load or save problem, for the UI
	nextID int
}

// Dir returns the configuration directory: $KKDESK_CONFIG_DIR, else
// os.UserConfigDir()/kkdesk.
func Dir() (string, error) {
	if d := os.Getenv(EnvDir); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "kkdesk"), nil
}

// Open loads dir/kkdesk.json. It always returns a usable store: when the
// file is missing it holds the defaults (the file is created by the first
// change); when the file is corrupt it is moved to kkdesk.json.bad, the
// defaults are used and the error (ErrCorrupt) is also kept for Err; when
// it cannot be read the defaults are used and the error returned.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName), f: file{Version: 1, Settings: Defaults()}}
	err := s.load()
	if err != nil {
		s.errMsg = errText(err)
	}
	s.normalize()
	return s, err
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: read %s: %w", s.path, err)
	}
	f := file{Settings: Defaults()}
	if err := json.Unmarshal(b, &f); err != nil {
		bad := s.path + ".bad"
		if rerr := os.Rename(s.path, bad); rerr != nil {
			return fmt.Errorf("%w (%v); moving it to %s failed: %v", ErrCorrupt, err, bad, rerr)
		}
		return fmt.Errorf("%w (%v); moved to %s", ErrCorrupt, err, bad)
	}
	f.Version = 1
	s.f = f
	return nil
}

// normalize repairs the loaded document: valid settings, the default group
// first, no duplicate groups or addresses, every device in a known group.
func (s *Store) normalize() {
	s.f.Settings.repair()
	groups := []string{DefaultGroup}
	for _, g := range s.f.Groups {
		if g = cleanName(g); g != "" && !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	s.f.Groups = groups
	var devs []Device
	seen := map[string]bool{}
	for _, d := range s.f.Devices {
		d.Addr = NormalizeAddr(d.Addr)
		if d.Addr == "" || seen[d.Addr] {
			continue
		}
		seen[d.Addr] = true
		d.fill()
		if !slices.Contains(s.f.Groups, d.Group) {
			s.f.Groups = append(s.f.Groups, d.Group)
		}
		devs = append(devs, d)
	}
	s.f.Devices = devs
	for _, d := range devs {
		var n int
		if _, err := fmt.Sscanf(d.ID, "d%d", &n); err == nil && n > s.nextID {
			s.nextID = n
		}
	}
	for i := range s.f.Devices {
		if s.f.Devices[i].ID == "" {
			s.f.Devices[i].ID = s.newIDLocked()
		}
	}
}

// Path returns the configuration file path.
func (s *Store) Path() string { return s.path }

// Err returns the last load or save problem in words ("" when the file is
// in sync with memory).
func (s *Store) Err() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errMsg
}

// Settings returns the current settings.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Settings
}

// UpdateSettings changes the settings and saves them. Invalid values are
// replaced by their defaults. On a save error the change stays in memory.
func (s *Store) UpdateSettings(fn func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.f.Settings)
	s.f.Settings.repair()
	return s.saveLocked()
}

// TempPassword returns the saved temporary password (zero when none).
func (s *Store) TempPassword() TempPassword {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f.Temp == nil {
		return TempPassword{}
	}
	return *s.f.Temp
}

// SetTempPassword saves the current temporary password.
func (s *Store) SetTempPassword(t TempPassword) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.f.Temp = &t
	return s.saveLocked()
}

// saveLocked writes the document atomically: a temporary file in the same
// directory, synced, then renamed over the old one (os.Rename replaces it
// on Windows too). os.CreateTemp makes the file private (0600): it holds
// the passwords.
func (s *Store) saveLocked() error {
	err := s.writeLocked()
	if err != nil {
		s.errMsg = errText(err)
	} else {
		s.errMsg = ""
	}
	return err
}

func (s *Store) writeLocked() error {
	b, err := json.MarshalIndent(s.f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ok = true
	return nil
}

// errText is a load or save error in words for the UI.
func errText(err error) string {
	switch {
	case errors.Is(err, ErrCorrupt):
		return "配置文件已损坏，已备份为 " + FileName + ".bad 并恢复默认设置"
	case errors.Is(err, os.ErrPermission):
		return "无法保存配置（没有写入权限），改动只在本次运行有效"
	}
	return "配置读写失败：" + err.Error()
}
