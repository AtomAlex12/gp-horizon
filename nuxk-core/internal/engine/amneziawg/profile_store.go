package amneziawg

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrInvalidProfile = errors.New("недопустимый профиль WG / AWG")

// Installed checks the Entware tools without starting a process.
func Installed() bool {
	_, daemonErr := exec.LookPath("amneziawg-go")
	_, toolErr := exec.LookPath("awg")
	return daemonErr == nil && toolErr == nil
}

func (a *Adapter) ProfileConfigured() bool {
	info, err := os.Lstat(a.config)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && info.Size() > 0
}

// SaveProfile atomically replaces the private file after validation.
func (a *Adapter) SaveProfile(config string) error {
	if len(config) > MaxProfileSize {
		return fmt.Errorf("%w: файл больше 32 КиБ", ErrInvalidProfile)
	}
	if _, err := parseProfile(strings.NewReader(config)); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidProfile, err)
	}
	if err := writePrivateProfile(a.config, config); err != nil {
		return errors.New("не удалось сохранить профиль на роутере")
	}
	return nil
}

func writePrivateProfile(path, config string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".warp-awg-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.WriteString(config); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
