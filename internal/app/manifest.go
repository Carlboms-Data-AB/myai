package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Manifest records what MyAI installed on this machine, so uninstall can
// remove exactly that and nothing more. Something that was already present
// when MyAI arrived is not MyAI's to delete.
type Manifest struct {
	// Backend is true when MyAI installed the inference backend.
	Backend bool `toml:"backend"`
	// BackendName is the backend it installed, for the uninstall plan.
	BackendName string `toml:"backend_name"`
	// OpenCode is true when MyAI downloaded OpenCode.
	OpenCode bool `toml:"opencode"`
	// BrowserSkill is true when MyAI installed the browser automation skill.
	BrowserSkill bool `toml:"browser_skill"`
}

// manifestFile is where the record lives.
func (a *App) manifestFile() string {
	return filepath.Join(a.env.Config, "installed.toml")
}

// LoadManifest reads the record of what MyAI installed. A missing file means
// nothing is recorded, which is the safe answer: uninstall then removes only
// MyAI's own files.
func (a *App) LoadManifest() Manifest {
	var m Manifest

	data, err := os.ReadFile(a.manifestFile())
	if errors.Is(err, os.ErrNotExist) || err != nil {
		return m
	}
	if err := toml.Unmarshal(data, &m); err != nil {
		return Manifest{}
	}
	return m
}

// saveManifest persists the record.
func (a *App) saveManifest(m Manifest) error {
	if err := os.MkdirAll(a.env.Config, 0o755); err != nil {
		return err
	}

	f, err := os.CreateTemp(a.env.Config, ".installed-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())

	if err := toml.NewEncoder(f).Encode(m); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), a.manifestFile())
}

// recordInstalled notes that MyAI installed something.
func (a *App) recordInstalled(update func(*Manifest)) error {
	m := a.LoadManifest()
	update(&m)
	return a.saveManifest(m)
}
