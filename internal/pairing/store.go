package pairing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const Lifetime = 3 * time.Minute

var ErrExpired = errors.New("pairing code expired or already used")

type Phone struct {
	PublicKey    string `json:"publicKey"`
	Name         string `json:"name"`
	Model        string `json:"model,omitempty"`
	Bypass       bool   `json:"bypass,omitempty"`
	BiometricKey string `json:"biometricKey,omitempty"`
}

type Invite struct {
	Code    string
	Manual  string
	Expires time.Time
}

type Store struct {
	key         ed25519.PrivateKey
	public      string
	id          string
	fingerprint string
	path        string
	mu          sync.Mutex
	invite      Invite
	phones      []Phone
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "desktop-identity.key")
	seed, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, err
		}
		if err := writeFile(path, seed, true); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		seed, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid desktop identity key")
	}
	key := ed25519.NewKeyFromSeed(seed)
	pub := key.Public().(ed25519.PublicKey)
	s := &Store{key: key, public: Encode(pub), id: DeviceID(pub), fingerprint: Fingerprint(pub),
		path: filepath.Join(dir, "paired-phones.json")}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &s.phones)
	}
	return s, err
}

func writeFile(path string, data []byte, create bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pairing-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if create {
		return os.Link(f.Name(), path)
	}
	return os.Rename(f.Name(), path)
}

func Fingerprint(public []byte) string {
	hash := sha256.Sum256(public)
	return hex.EncodeToString(hash[:])
}

func DeviceID(public []byte) string {
	hash := sha256.Sum256(append([]byte("kkdesk desktop id v1\x00"), public...))
	return Encode(hash[:16])
}

func (s *Store) ID() string          { return s.id }
func (s *Store) PublicKey() string   { return s.public }
func (s *Store) Fingerprint() string { return s.fingerprint }

func (s *Store) DeviceCode() string {
	public, _ := PublicKey(s.public)
	return DeviceCode(public)
}

func DeviceCode(public []byte) string {
	sum := sha256.Sum256([]byte(DeviceID(public)))
	return "KD-" + base32.StdEncoding.EncodeToString(sum[:])[:12]
}

func (s *Store) Invite(now time.Time) Invite {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invite.Code == "" || !now.Before(s.invite.Expires) {
		var manual [10]byte
		_, _ = rand.Read(manual[:])
		s.invite = Invite{Code: rand.Text(), Manual: base32.StdEncoding.EncodeToString(manual[:]), Expires: now.Add(Lifetime)}
	}
	return s.invite
}

func (s *Store) CurrentInvite(now time.Time) Invite {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !now.Before(s.invite.Expires) {
		return Invite{}
	}
	return s.invite
}

func (s *Store) ManualHostProof(tx Transcript, now time.Time) string {
	invite := s.CurrentInvite(now)
	if invite.Manual == "" {
		return ""
	}
	return tx.ManualHostProof(invite.Manual)
}

func (s *Store) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invite = Invite{}
}

func (s *Store) Claim(t Transcript, proof string, now time.Time) (Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := s.invite.Code
	if t.Purpose == "pair_manual" {
		code = s.invite.Manual
	}
	if code == "" || !now.Before(s.invite.Expires) ||
		subtle.ConstantTimeCompare([]byte(t.CodeProof(code)), []byte(proof)) != 1 {
		return Invite{}, ErrExpired
	}
	invite := s.invite
	invite.Code = code
	s.invite = Invite{}
	return invite, nil
}

func (s *Store) Sign(t Transcript, m *Message) { t.Sign(s.key, m) }

func (s *Store) Phones() []Phone {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.phones)
}

func (s *Store) Phone(key string) (Phone, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.phones {
		if p.PublicKey == key {
			return p, true
		}
	}
	return Phone{}, false
}

func (s *Store) save(phones []Phone) error {
	data, err := json.MarshalIndent(phones, "", "  ")
	if err == nil {
		err = writeFile(s.path, data, false)
	}
	if err == nil {
		s.phones = phones
	}
	return err
}

func (s *Store) Save(phone Phone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := PublicKey(phone.PublicKey); !ok {
		return errors.New("invalid phone public key")
	}
	if slices.ContainsFunc(s.phones, func(p Phone) bool { return p.PublicKey == phone.PublicKey }) {
		return fmt.Errorf("此手机已配对")
	}
	return s.save(append(slices.Clone(s.phones), phone))
}

func (s *Store) Update(key string, update func(*Phone)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	phones := slices.Clone(s.phones)
	for i := range phones {
		if phones[i].PublicKey == key {
			update(&phones[i])
			return s.save(phones)
		}
	}
	return errors.New("phone has been unpaired")
}

func (s *Store) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	phones := slices.DeleteFunc(slices.Clone(s.phones), func(p Phone) bool { return p.PublicKey == key })
	if len(phones) == len(s.phones) {
		return nil
	}
	return s.save(phones)
}
