package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tangoui/kkdesk/internal/rendezvous"
)

type serverConfig struct {
	Listen         string         `json:"listen"`
	Cert           string         `json:"tls-cert"`
	Key            string         `json:"tls-key"`
	TURNListen     string         `json:"turn-listen"`
	TURNTLSListen  string         `json:"turn-tls-listen"`
	TURNHost       string         `json:"turn-host"`
	PublicIP       string         `json:"turn-public-ip"`
	RelayBind      string         `json:"relay-bind"`
	MinPort        int            `json:"relay-min"`
	MaxPort        int            `json:"relay-max"`
	RelayOnly      bool           `json:"relay-only"`
	MaxConnections int            `json:"max-signaling-connections"`
	CredentialTTL  configDuration `json:"turn-credential-ttl"`
}

func parseConfig(args []string) (serverConfig, error) {
	var cfg serverConfig
	var configPath string
	flags := flag.NewFlagSet("kkdesk-server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&configPath, "config", "", "optional JSON configuration file")
	flags.StringVar(&cfg.Listen, "listen", "127.0.0.1:8080", "signaling listen address")
	flags.StringVar(&cfg.Cert, "tls-cert", "", "TLS certificate PEM including chain")
	flags.StringVar(&cfg.Key, "tls-key", "", "TLS private key PEM")
	flags.StringVar(&cfg.TURNListen, "turn-listen", "0.0.0.0:3478", "TURN UDP listen address")
	flags.StringVar(&cfg.TURNTLSListen, "turn-tls-listen", "", "optional TURN TLS listen address")
	flags.StringVar(&cfg.TURNHost, "turn-host", "", "public DNS name used by TURN TLS")
	flags.StringVar(&cfg.PublicIP, "turn-public-ip", "", "public IPv4 advertised for TURN allocations")
	flags.StringVar(&cfg.RelayBind, "relay-bind", "0.0.0.0", "local IPv4 to bind relay allocations")
	flags.IntVar(&cfg.MinPort, "relay-min", 49160, "first UDP relay port")
	flags.IntVar(&cfg.MaxPort, "relay-max", 49259, "last UDP relay port")
	flags.BoolVar(&cfg.RelayOnly, "relay-only", false, "require media to use the relay")
	opts := rendezvous.DefaultServerOptions()
	flags.IntVar(&cfg.MaxConnections, "max-signaling-connections", opts.MaxConnections, "maximum simultaneous signaling connections")
	flags.TextVar(&cfg.CredentialTTL, "turn-credential-ttl", configDuration(opts.CredentialTTL), "TURN credential lifetime, for example 2h")
	if err := flags.Parse(args); err != nil {
		return cfg, err
	}
	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if configPath != "" {
		overrides := make(map[string]string)
		flags.Visit(func(f *flag.Flag) { overrides[f.Name] = f.Value.String() })
		if err := loadConfig(configPath, &cfg); err != nil {
			return cfg, err
		}
		for name, value := range overrides {
			if err := flags.Set(name, value); err != nil {
				return cfg, err
			}
		}
	}
	if cfg.MaxConnections < 1 {
		return cfg, errors.New("max-signaling-connections must be positive")
	}
	if time.Duration(cfg.CredentialTTL) < time.Second {
		return cfg, errors.New("turn-credential-ttl must be at least 1s")
	}
	return cfg, nil
}

func loadConfig(path string, cfg *serverConfig) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	target := cfg
	if err := dec.Decode(&target); err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	if target == nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return fmt.Errorf("config %q must contain one JSON object", path)
	}
	return nil
}

type configDuration time.Duration

func (d configDuration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func (d *configDuration) UnmarshalText(data []byte) error {
	value, err := time.ParseDuration(string(data))
	if err != nil {
		return err
	}
	*d = configDuration(value)
	return nil
}

func (d *configDuration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return d.UnmarshalText([]byte(value))
}
