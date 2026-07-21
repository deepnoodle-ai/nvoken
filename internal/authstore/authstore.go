// Package authstore persists nvoken CLI credential profiles.
package authstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

var ErrNoDefaultProfile = errors.New("no default profile")

type Profile struct {
	Name         string `toml:"-"`
	Default      bool   `toml:"default,omitempty"`
	Endpoint     string `toml:"endpoint"`
	Token        string `toml:"token"`
	CredentialID string `toml:"credential_id"`
	AccountID    string `toml:"account_id"`
	SubjectID    string `toml:"subject_id,omitempty"`
	Subject      string `toml:"subject,omitempty"`
	CreatedAt    string `toml:"created_at"`
	LastUsedAt   string `toml:"last_used_at,omitempty"`
}

type Store struct {
	Profiles map[string]Profile
}

var (
	pathMu       sync.RWMutex
	pathOverride string
)

func SetPathOverride(path string) {
	pathMu.Lock()
	defer pathMu.Unlock()
	pathOverride = path
}

func Path() (string, error) {
	pathMu.RLock()
	override := pathOverride
	pathMu.RUnlock()
	if override == "" {
		override = os.Getenv("NVOKEN_CREDENTIALS_FILE")
	}
	if override != "" {
		if filepath.IsAbs(override) {
			return override, nil
		}
		absolute, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve credentials file: %w", err)
		}
		return absolute, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate user home dir: %w", err)
	}
	return filepath.Join(home, ".nvoken", "credentials"), nil
}

func PermissionWarning() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Sprintf("%s has permissions %04o; run `chmod 600 %s`", path, info.Mode().Perm(), path), nil
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err == nil && dirInfo.Mode().Perm()&0o077 != 0 {
		return fmt.Sprintf("%s has permissions %04o; run `chmod 700 %s`", filepath.Dir(path), dirInfo.Mode().Perm(), filepath.Dir(path)), nil
	}
	return "", nil
}

func LoadStore() (*Store, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Store{Profiles: map[string]Profile{}}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	profiles := map[string]Profile{}
	if err := toml.Unmarshal(data, &profiles); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &Store{Profiles: profiles}, nil
}

func SaveStore(store *Store) error {
	if store == nil || store.Profiles == nil {
		store = &Store{Profiles: map[string]Profile{}}
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credentials dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("secure credentials dir: %w", err)
	}
	data, err := toml.Marshal(store.Profiles)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary credentials file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := os.Chmod(temporaryName, 0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace credentials file: %w", err)
	}
	return nil
}

func ResolveProfile(name string) (*Profile, error) {
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	if name != "" {
		profile, ok := store.Profiles[name]
		if !ok {
			return nil, fmt.Errorf("profile %q not found; run `nvoken auth login --profile %s`", name, name)
		}
		profile.Name = name
		return &profile, nil
	}
	defaults := []string{}
	for profileName, profile := range store.Profiles {
		if profile.Default {
			defaults = append(defaults, profileName)
		}
	}
	sort.Strings(defaults)
	switch len(defaults) {
	case 0:
		return nil, fmt.Errorf("%w; run `nvoken auth login --profile <name>` or `nvoken auth use <name>`", ErrNoDefaultProfile)
	case 1:
		profile := store.Profiles[defaults[0]]
		profile.Name = defaults[0]
		return &profile, nil
	default:
		return nil, fmt.Errorf("multiple default profiles: %s; run `nvoken auth use <name>` to repair", strings.Join(defaults, ", "))
	}
}

func PutProfile(name string, profile Profile, makeDefault bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("profile name is required")
	}
	store, err := LoadStore()
	if err != nil {
		return err
	}
	existing, exists := store.Profiles[name]
	profile.Name = ""
	switch {
	case makeDefault:
		for otherName, other := range store.Profiles {
			other.Default = false
			store.Profiles[otherName] = other
		}
		profile.Default = true
	case exists:
		profile.Default = existing.Default
	default:
		profile.Default = false
	}
	store.Profiles[name] = profile
	if !hasDefault(store) {
		profile.Default = true
		store.Profiles[name] = profile
	}
	return SaveStore(store)
}

func SetDefault(name string) error {
	store, err := LoadStore()
	if err != nil {
		return err
	}
	if _, ok := store.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	for profileName, profile := range store.Profiles {
		profile.Default = profileName == name
		store.Profiles[profileName] = profile
	}
	return SaveStore(store)
}

func DeleteProfile(name string) error {
	store, err := LoadStore()
	if err != nil {
		return err
	}
	if _, ok := store.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	delete(store.Profiles, name)
	return SaveStore(store)
}

func TouchProfile(name, when string) error {
	if name == "" || when == "" {
		return nil
	}
	store, err := LoadStore()
	if err != nil {
		return err
	}
	profile, ok := store.Profiles[name]
	if !ok {
		return nil
	}
	profile.LastUsedAt = when
	store.Profiles[name] = profile
	return SaveStore(store)
}

func hasDefault(store *Store) bool {
	for _, profile := range store.Profiles {
		if profile.Default {
			return true
		}
	}
	return false
}
