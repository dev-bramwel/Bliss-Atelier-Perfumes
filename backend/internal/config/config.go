// Package config validates Go process configuration without exposing values.
package config

import (
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address         string
	DatabaseURL     string
	IOTimeout       time.Duration
	ShutdownTimeout time.Duration
	MaxConnections  int32
	RequireSchema   bool
}

// Load uses separate GO_ settings so the legacy backend remains independent.
func Load(get func(string) string) (Config, error) {
	c := Config{Address: "127.0.0.1:5001", IOTimeout: 5 * time.Second, ShutdownTimeout: 10 * time.Second, MaxConnections: 4}
	if v := get("GO_HTTP_ADDR"); v != "" {
		c.Address = v
	}
	host, port, err := net.SplitHostPort(c.Address)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 1 || n > 65535 || (host != "localhost" && net.ParseIP(host) == nil) {
		return Config{}, errors.New("GO_HTTP_ADDR must contain an IP or localhost and a valid port")
	}
	for _, entry := range []struct {
		key    string
		target *time.Duration
	}{{"GO_IO_TIMEOUT", &c.IOTimeout}, {"GO_SHUTDOWN_TIMEOUT", &c.ShutdownTimeout}} {
		if v := get(entry.key); v != "" {
			d, e := time.ParseDuration(v)
			if e != nil || d < time.Millisecond || d > time.Minute {
				return Config{}, errors.New(entry.key + " must be between 1ms and 1m")
			}
			*entry.target = d
		}
	}
	if v := get("GO_DB_MAX_CONNECTIONS"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 32 {
			return Config{}, errors.New("GO_DB_MAX_CONNECTIONS must be between 1 and 32")
		}
		c.MaxConnections = int32(n)
	}
	c.DatabaseURL = get("GO_DATABASE_URL")
	if path := get("GO_DATABASE_URL_FILE"); path != "" {
		if c.DatabaseURL != "" {
			return Config{}, errors.New("set only one of GO_DATABASE_URL and GO_DATABASE_URL_FILE")
		}
		file, e := os.Open(path)
		if e != nil {
			return Config{}, errors.New("GO_DATABASE_URL_FILE could not be read")
		}
		data, e := io.ReadAll(io.LimitReader(file, 8193))
		_ = file.Close()
		if e != nil || len(data) > 8192 {
			return Config{}, errors.New("GO_DATABASE_URL_FILE could not be read")
		}
		c.DatabaseURL = strings.TrimSpace(string(data))
		if c.DatabaseURL == "" {
			return Config{}, errors.New("GO_DATABASE_URL_FILE is empty")
		}
	}
	if v := get("GO_REQUIRE_SCHEMA"); v != "" {
		if v != "true" && v != "false" {
			return Config{}, errors.New("GO_REQUIRE_SCHEMA must be true or false")
		}
		c.RequireSchema = v == "true"
	}
	if c.RequireSchema && c.DatabaseURL == "" {
		return Config{}, errors.New("GO_REQUIRE_SCHEMA requires database configuration")
	}
	if c.DatabaseURL != "" {
		u, e := url.Parse(c.DatabaseURL)
		if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), "% /\\") || (net.ParseIP(u.Hostname()) == nil && !regexp.MustCompile(`^[a-zA-Z0-9.-]+$`).MatchString(u.Hostname())) || u.Path == "" || u.Path == "/" {
			return Config{}, errors.New("GO_DATABASE_URL must be a PostgreSQL URL with a host and database")
		}
	}
	return c, nil
}
