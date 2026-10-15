package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	mobyclient "github.com/moby/moby/client"
)

// DefaultContext is the context the docker CLI always has: $DOCKER_HOST,
// or the local socket.
const DefaultContext = "default"

// Context is a docker CLI context (`docker context ls`). zdocker only
// reads them: choosing one is in-memory state, the CLI config is never
// written — the same rule kboba follows for kubeconfig.
type Context struct {
	Name        string
	Description string
	Host        string // unix:///…, tcp://…, ssh://…
	TLSDir      string // ca.pem/cert.pem/key.pem, if the context has TLS material
	Current     bool
}

// ConfigDir is where the docker CLI keeps its config: $DOCKER_CONFIG, or
// ~/.docker.
func ConfigDir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker")
}

// contextMeta is the part of contexts/meta/<id>/meta.json zdocker uses.
type contextMeta struct {
	Name     string `json:"Name"`
	Metadata struct {
		Description string `json:"Description"`
	} `json:"Metadata"`
	Endpoints map[string]struct {
		Host string `json:"Host"`
	} `json:"Endpoints"`
}

// contextID is how the CLI names a context's directory: the SHA-256 of
// its name (names can contain characters unsafe in paths).
func contextID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// ListContexts reads the CLI's contexts from configDir, "default" first,
// and marks the current one: $DOCKER_CONTEXT, else config.json's
// currentContext, else "default". $DOCKER_HOST makes "default" current,
// as with the CLI.
func ListContexts(configDir string) ([]Context, error) {
	def := Context{Name: DefaultContext, Description: "Current DOCKER_HOST based configuration", Host: os.Getenv("DOCKER_HOST")}
	if def.Host == "" {
		def.Host = mobyclient.DefaultDockerHost
	}
	out := []Context{def}

	metaDir := filepath.Join(configDir, "contexts", "meta")
	entries, err := os.ReadDir(metaDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(metaDir, e.Name(), "meta.json"))
		if err != nil {
			continue // not a context directory
		}
		var m contextMeta
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("context %s: %w", e.Name(), err)
		}
		c := Context{Name: m.Name, Description: m.Metadata.Description, Host: m.Endpoints["docker"].Host}
		tls := filepath.Join(configDir, "contexts", "tls", contextID(m.Name), "docker")
		if _, err := os.Stat(filepath.Join(tls, "ca.pem")); err == nil {
			c.TLSDir = tls
		}
		out = append(out, c)
	}
	slices.SortFunc(out[1:], func(a, b Context) int { return strings.Compare(a.Name, b.Name) })

	current := currentContextName(configDir)
	for i := range out {
		out[i].Current = out[i].Name == current
	}
	return out, nil
}

func currentContextName(configDir string) string {
	if os.Getenv("DOCKER_HOST") != "" {
		return DefaultContext
	}
	if c := os.Getenv("DOCKER_CONTEXT"); c != "" {
		return c
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		return DefaultContext
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if json.Unmarshal(raw, &cfg) != nil || cfg.CurrentContext == "" {
		return DefaultContext
	}
	return cfg.CurrentContext
}

// NewForContext connects to a context's endpoint. The default context
// reads the environment like the CLI; others use their own host and TLS
// files and ignore DOCKER_HOST.
func NewForContext(c Context) (Client, error) {
	if c.Name == DefaultContext {
		return New("")
	}
	if strings.HasPrefix(c.Host, "ssh://") {
		return nil, fmt.Errorf("context %q uses ssh (%s): not supported yet; use a tcp:// or unix:// context, or DOCKER_HOST", c.Name, c.Host)
	}
	opts := []mobyclient.Opt{mobyclient.WithHost(c.Host)}
	if c.TLSDir != "" {
		opts = append(opts, mobyclient.WithTLSClientConfig(
			filepath.Join(c.TLSDir, "ca.pem"), filepath.Join(c.TLSDir, "cert.pem"), filepath.Join(c.TLSDir, "key.pem")))
	}
	api, err := mobyclient.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", c.Name, err)
	}
	return &client{api: api}, nil
}

// FindContext returns the context named name.
func FindContext(contexts []Context, name string) (Context, bool) {
	i := slices.IndexFunc(contexts, func(c Context) bool { return c.Name == name })
	if i < 0 {
		return Context{}, false
	}
	return contexts[i], true
}
