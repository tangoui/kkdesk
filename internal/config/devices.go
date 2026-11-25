package config

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultGroup is the group of new devices.
const DefaultGroup = "我的电脑"

// Errors of the address book.
var (
	ErrDuplicate   = errors.New("config: already exists")
	ErrNotFound    = errors.New("config: no such device")
	ErrInvalidAddr = errors.New("config: invalid address")
)

// Device is one entry of 我的设备.
type Device struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Addr          string    `json:"addr"` // host:port, see NormalizeAddr
	Group         string    `json:"group"`
	OS            string    `json:"os,omitempty"`
	LastConnected time.Time `json:"lastConnected,omitzero"`
	Note          string    `json:"note,omitempty"`
}

// fill gives an entry its default name and group.
func (d *Device) fill() {
	d.Name, d.Group = cleanName(d.Name), cleanName(d.Group)
	if d.Name == "" {
		d.Name = DisplayAddr(d.Addr)
	}
	if d.Group == "" {
		d.Group = DefaultGroup
	}
}

func cleanName(s string) string { return strings.TrimSpace(s) }

// NormalizeAddr returns addr as host:port with the default port added ("" if
// addr is empty or malformed). Entries are deduplicated by this form.
func NormalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" || port == "" {
			return ""
		}
		if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
			return ""
		}
		return net.JoinHostPort(strings.ToLower(host), port)
	}
	host := strings.Trim(addr, "[]")
	if host == "" || strings.ContainsAny(host, " /") {
		return ""
	}
	return net.JoinHostPort(strings.ToLower(host), strconv.Itoa(DefaultPort))
}

// DisplayAddr is addr without the default port.
func DisplayAddr(addr string) string {
	if host, port, err := net.SplitHostPort(addr); err == nil && port == strconv.Itoa(DefaultPort) {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return addr
}

func (s *Store) newIDLocked() string {
	s.nextID++
	return fmt.Sprintf("d%d", s.nextID)
}

// Devices returns a copy of the address book.
func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.f.Devices)
}

// Device returns one entry.
func (s *Store) Device(id string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.indexLocked(id); i >= 0 {
		return s.f.Devices[i], true
	}
	return Device{}, false
}

func (s *Store) indexLocked(id string) int {
	return slices.IndexFunc(s.f.Devices, func(d Device) bool { return d.ID == id })
}

// Groups returns the groups, DefaultGroup first.
func (s *Store) Groups() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.f.Groups)
}

// AddGroup creates a group.
func (s *Store) AddGroup(name string) error {
	name = cleanName(name)
	if name == "" {
		return errors.New("分组名称不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.f.Groups, name) {
		return fmt.Errorf("分组「%s」已存在: %w", name, ErrDuplicate)
	}
	s.f.Groups = append(s.f.Groups, name)
	return s.saveLocked()
}

// Connected records a successful connection to addr: the entry with that
// address gets the time, or a new entry is added (named after the
// address, in DefaultGroup).
func (s *Store) Connected(addr string, at time.Time) (Device, error) {
	n := NormalizeAddr(addr)
	if n == "" {
		return Device{}, ErrInvalidAddr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.f.Devices, func(d Device) bool { return d.Addr == n })
	if i < 0 {
		d := Device{ID: s.newIDLocked(), Addr: n}
		d.fill()
		s.f.Devices = append(s.f.Devices, d)
		i = len(s.f.Devices) - 1
	}
	s.f.Devices[i].LastConnected = at
	return s.f.Devices[i], s.saveLocked()
}

// AddDevice adds an entry by hand. An address already in the book is
// ErrDuplicate.
func (s *Store) AddDevice(name, addr, group string) (Device, error) {
	n := NormalizeAddr(addr)
	if n == "" {
		return Device{}, ErrInvalidAddr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.f.Devices, func(d Device) bool { return d.Addr == n }) {
		return Device{}, fmt.Errorf("%s 已在设备列表中: %w", DisplayAddr(n), ErrDuplicate)
	}
	d := Device{ID: s.newIDLocked(), Name: name, Addr: n, Group: group}
	d.fill()
	s.addGroupLocked(d.Group)
	s.f.Devices = append(s.f.Devices, d)
	return d, s.saveLocked()
}

func (s *Store) addGroupLocked(g string) {
	if !slices.Contains(s.f.Groups, g) {
		s.f.Groups = append(s.f.Groups, g)
	}
}

// UpdateDevice edits an entry (rename, move to a group, note). The ID and
// address cannot change; an empty name or group falls back to the default
// and an unknown group is created.
func (s *Store) UpdateDevice(id string, fn func(*Device)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return ErrNotFound
	}
	d := s.f.Devices[i]
	fn(&d)
	d.ID, d.Addr = s.f.Devices[i].ID, s.f.Devices[i].Addr
	d.fill()
	s.addGroupLocked(d.Group)
	s.f.Devices[i] = d
	return s.saveLocked()
}

// DeleteDevice removes an entry.
func (s *Store) DeleteDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return ErrNotFound
	}
	s.f.Devices = slices.Delete(s.f.Devices, i, i+1)
	return s.saveLocked()
}
