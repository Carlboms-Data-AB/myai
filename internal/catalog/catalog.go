// Package catalog maps logical models such as "Qwen3.5 9B" onto the concrete
// artifact each platform needs. Callers ask for a model by id and get back the
// right Hugging Face repository, file and backend for the host, so nobody
// using MyAI has to know whether their machine runs MLX or GGUF.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// Backend identifiers. These match the values in config.
const (
	BackendMLXServe = "mlx-serve"
	BackendLlamaCPP = "llama.cpp"
)

// Model is a logical model offered by MyAI.
type Model struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Context and Output are the limits advertised to OpenCode.
	Context   int        `json:"context"`
	Output    int        `json:"output"`
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact is the platform-specific realisation of a Model.
type Artifact struct {
	// OS and Arch list the platforms this artifact serves. An empty list
	// matches any value.
	OS   []string `json:"os"`
	Arch []string `json:"arch"`
	// Backend is the inference backend that loads this artifact.
	Backend string `json:"backend"`
	// Repo is the Hugging Face repository id.
	Repo string `json:"repo"`
	// File is the specific file to download. GGUF artifacts name one file;
	// MLX artifacts use the whole repository and leave this empty.
	File string `json:"file"`
	// Quant is the human-readable quantization label.
	Quant string `json:"quant"`
	// Size is the on-disk size in bytes, used for disk-space checks.
	Size int64 `json:"size"`
	// Custom marks an artifact the user supplied rather than one MyAI ships.
	Custom bool `json:"-"`
}

// Target is what a model has to be resolved for: a platform, and optionally a
// specific backend. Pinning the backend matters because some platforms can run
// more than one, and the artifact has to match whichever will actually load it.
type Target struct {
	// OS and Arch are the platform, as in runtime.GOOS and runtime.GOARCH.
	OS, Arch string
	// Backend restricts resolution to artifacts that backend can load. An
	// empty value takes whichever artifact fits the platform best.
	Backend string
}

// HostTarget returns a Target for a platform with no backend pinned.
func HostTarget(goos, goarch string) Target { return Target{OS: goos, Arch: goarch} }

// WithBackend returns a copy of the target pinned to a backend.
func (t Target) WithBackend(backend string) Target {
	t.Backend = backend
	return t
}

// Resolved pairs a logical model with the artifact chosen for a platform.
type Resolved struct {
	Model    Model
	Artifact Artifact
}

// Backend reports which inference backend serves this model.
func (r Resolved) Backend() string { return r.Artifact.Backend }

// Ref is the stable identity of an artifact on disk: the repository for MLX,
// or repository and file for GGUF.
func (r Resolved) Ref() string {
	if r.Artifact.File != "" {
		return r.Artifact.Repo + "/" + r.Artifact.File
	}
	return r.Artifact.Repo
}

// Label describes the resolved artifact for display.
func (r Resolved) Label() string {
	if r.Artifact.Quant != "" {
		return fmt.Sprintf("%s (%s)", r.Model.Name, r.Artifact.Quant)
	}
	return r.Model.Name
}

type catalogFile struct {
	Models []Model `json:"models"`
}

var builtin catalogFile

func init() {
	if err := json.Unmarshal(catalogJSON, &builtin); err != nil {
		// The catalog is embedded at build time, so a parse failure is a bug
		// in this package rather than a runtime condition.
		panic("catalog: embedded catalog.json is invalid: " + err.Error())
	}
}

// All returns every logical model MyAI knows about, in a stable order.
func All() []Model {
	out := make([]Model, len(builtin.Models))
	copy(out, builtin.Models)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Available returns the models that have an artifact for the given target.
func Available(t Target) []Resolved {
	var out []Resolved
	for _, m := range All() {
		if a, ok := m.artifactFor(t); ok {
			out = append(out, Resolved{Model: m, Artifact: a})
		}
	}
	return out
}

// Lookup finds a logical model by id.
func Lookup(id string) (Model, bool) {
	for _, m := range builtin.Models {
		if strings.EqualFold(m.ID, id) {
			return m, true
		}
	}
	return Model{}, false
}

func (m Model) artifactFor(t Target) (Artifact, bool) {
	for _, a := range m.Artifacts {
		if !matches(a.OS, t.OS) || !matches(a.Arch, t.Arch) {
			continue
		}
		if t.Backend != "" && !strings.EqualFold(a.Backend, t.Backend) {
			continue
		}
		return a, true
	}
	return Artifact{}, false
}

func matches(list []string, value string) bool {
	if len(list) == 0 {
		return true
	}
	for _, v := range list {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}

// Resolve turns a configured model value into the artifact this platform
// needs. The value is either a catalog id or, for advanced use, a direct
// reference such as "mlx-community/Some-Model-6bit" or
// "unsloth/Some-Model-GGUF/Some-Model-Q6_K.gguf".
func Resolve(value string, t Target) (Resolved, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Resolved{}, fmt.Errorf("no model specified")
	}

	if m, ok := Lookup(value); ok {
		a, ok := m.artifactFor(t)
		if !ok {
			if t.Backend != "" {
				return Resolved{}, fmt.Errorf("model %q has no artifact for %s/%s on the %s backend", m.Name, t.OS, t.Arch, t.Backend)
			}
			return Resolved{}, fmt.Errorf("model %q has no artifact for %s/%s", m.Name, t.OS, t.Arch)
		}
		return Resolved{Model: m, Artifact: a}, nil
	}

	return resolveCustom(value, t)
}

// DefaultBackend reports which backend a platform uses when the user has not
// pinned one.
func DefaultBackend(goos, goarch string) string {
	if goos == "darwin" && goarch == "arm64" {
		return BackendMLXServe
	}
	return BackendLlamaCPP
}

// resolveCustom builds a synthetic model from a user-supplied reference.
func resolveCustom(value string, t Target) (Resolved, error) {
	backend := t.Backend
	if backend == "" {
		backend = DefaultBackend(t.OS, t.Arch)
	}

	repo, file, quant, err := ParseRef(value, backend)
	if err != nil {
		return Resolved{}, err
	}

	name := repo
	if file != "" {
		name = strings.TrimSuffix(file, ".gguf")
	}
	return Resolved{
		Model: Model{
			ID:      value,
			Name:    name,
			Summary: "Model supplied directly rather than from the MyAI catalog.",
		},
		Artifact: Artifact{
			Backend: backend,
			Repo:    repo,
			File:    file,
			Quant:   quant,
			Custom:  true,
		},
	}, nil
}

// ParseRef splits a direct model reference into its parts. Accepted forms are
// "org/repo", "org/repo:QUANT" and "org/repo/file.gguf".
func ParseRef(value, backend string) (repo, file, quant string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", "", fmt.Errorf("empty model reference")
	}
	if strings.ContainsAny(value, ` "'\`) {
		return "", "", "", fmt.Errorf("invalid model reference %q", value)
	}

	if base, q, ok := strings.Cut(value, ":"); ok {
		value, quant = base, q
	}

	parts := strings.Split(value, "/")
	switch {
	case len(parts) == 2:
		repo = value
	case len(parts) == 3 && strings.HasSuffix(strings.ToLower(parts[2]), ".gguf"):
		repo = parts[0] + "/" + parts[1]
		file = parts[2]
	default:
		return "", "", "", fmt.Errorf("model reference %q must look like org/repo, org/repo:QUANT or org/repo/file.gguf", value)
	}

	for _, p := range strings.Split(repo, "/") {
		if p == "" {
			return "", "", "", fmt.Errorf("invalid model reference %q", value)
		}
	}
	if backend == BackendMLXServe && file != "" {
		return "", "", "", fmt.Errorf("the MLX backend loads a whole repository, not a single file")
	}
	return repo, file, quant, nil
}
