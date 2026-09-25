package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"github.com/zalando/go-keyring"
)

type Profile struct {
	KeyID     string `json:"key_id,omitempty"`
	APIURL    string `json:"api_url"`
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
}
type Config struct {
	Active   string             `json:"active"`
	Profiles map[string]Profile `json:"profiles"`
}
type Project struct {
	Workspace string `toml:"workspace,omitempty"`
	Service   string `toml:"service,omitempty"`
	Project   string `toml:"project,omitempty"`
}
type Store struct{ Directory string }

func NewStore() (Store, error) {
	dir := os.Getenv("RUNIVO_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return Store{}, err
		}
		dir = filepath.Join(base, "runivo")
	}
	return Store{dir}, nil
}
func (s Store) Load() (Config, error) {
	result := Config{Profiles: map[string]Profile{}}
	raw, err := os.ReadFile(filepath.Join(s.Directory, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, errors.New("invalid Runivo configuration; repair config.json before continuing")
	}
	if result.Profiles == nil {
		result.Profiles = map[string]Profile{}
	}
	return result, nil
}
func (s Store) Save(value Config) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(s.Directory, "config.json"), append(raw, '\n'), 0600, false)
}
func CredentialID(profile Profile) string {
	sum := sha256.Sum256([]byte(profile.APIURL + "\n" + profile.Workspace + "\n" + profile.KeyID))
	return hex.EncodeToString(sum[:])
}
func Token(profile Profile) (string, error) {
	token, err := keyring.Get("Runivo CLI", CredentialID(profile))
	if err != nil {
		return "", errors.New("no usable keychain credential; run runivo login, or set RUNIVO_API_KEY for CI")
	}
	return token, nil
}
func SetToken(profile Profile, token string) error {
	if err := keyring.Set("Runivo CLI", CredentialID(profile), token); err != nil {
		return errors.New("could not store the token in your OS keychain; use login --token-file PATH on a headless machine")
	}
	return nil
}
func DeleteToken(profile Profile) error {
	err := keyring.Delete("Runivo CLI", CredentialID(profile))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
func LoadProject() (Project, error) {
	var p Project
	raw, err := os.ReadFile("runivo.toml")
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if len(raw) > 65536 {
		return p, errors.New("runivo.toml exceeds 64 KiB")
	}
	if err = toml.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("invalid runivo.toml: %w", err)
	}
	return p, nil
}
func AtomicWrite(path string, raw []byte, mode os.FileMode, exclusive bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if exclusive {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, err = file.Write(raw)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".runivo-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err = temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
