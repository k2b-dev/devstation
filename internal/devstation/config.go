package devstation

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type Config struct {
	Domain      string `toml:"domain"`
	Listen      string `toml:"listen"`
	Certificate string `toml:"certificate"`
	Key         string `toml:"key"`
	dir         string
}

func LoadConfig(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read configuration: %w", err)
	}
	if err = toml.NewDecoder(strings.NewReader(string(data))).DisallowUnknownFields().Decode(&c); err != nil {
		return c, err
	}
	if len(c.Domain) > 189 || !strings.Contains(c.Domain, ".") {
		return c, fmt.Errorf("domain must be a DNS suffix of at most 189 characters")
	}
	for _, label := range strings.Split(c.Domain, ".") {
		if !labelPattern.MatchString(label) {
			return c, fmt.Errorf("invalid domain %q: use lowercase ASCII DNS labels", c.Domain)
		}
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8443"
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil || net.ParseIP(host) == nil {
		return c, fmt.Errorf("listen must be an explicit IP:port (IPv6 in brackets)")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("invalid listen port")
	}
	if !filepath.IsAbs(c.Certificate) || !filepath.IsAbs(c.Key) {
		return c, fmt.Errorf("certificate and key must be absolute paths to an existing wildcard TLS certificate and private key")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return c, err
	}
	c.dir = filepath.Join(filepath.Dir(path), "state")
	// Unix-domain socket paths are limited to roughly 100 bytes across platforms.
	if len(c.socket()) > 100 {
		return c, fmt.Errorf("configuration path is too long for the Caddy admin socket")
	}
	return c, nil
}

func (c Config) socket() string    { return filepath.Join(c.dir, "admin.sock") }
func (c Config) statePath() string { return filepath.Join(c.dir, "caddy.json") }
func (c Config) url(name string) string {
	_, port, _ := net.SplitHostPort(c.Listen)
	suffix := ""
	if port != "443" {
		suffix = ":" + port
	}
	return "https://" + name + "." + c.Domain + suffix
}
