// Package rendezvous exchanges connection offers between authenticated devices.
package rendezvous

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pion/webrtc/v4"
)

const (
	path      = "/v1/signal"
	timeout   = 30 * time.Second
	readLimit = 128 << 10
)

type Config struct {
	URL   string
	Token string
}

func (c Config) endpoint() (string, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("请输入有效的跨网服务地址")
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("公网跨网服务需要 HTTPS")
		}
		u.Scheme = "ws"
	default:
		return "", errors.New("跨网服务地址需要以 https:// 开头")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = path
	}
	return u.String(), nil
}

func (c Config) Validate() error {
	if c.URL == "" && c.Token == "" {
		return nil
	}
	if _, err := c.endpoint(); err != nil {
		return err
	}
	if len(c.Token) < 32 || len(c.Token) > 256 || strings.ContainsAny(c.Token, " \t\r\n") {
		return errors.New("跨网服务密钥需要 32–256 个无空格字符")
	}
	return nil
}

func NewCode() string { return "KD-" + rand.Text()[:12] }

func NormalizeCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

func IsCode(code string) bool {
	code = NormalizeCode(code)
	return len(code) == 15 && strings.HasPrefix(code, "KD-") &&
		strings.Trim(code[3:], "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") == ""
}

type message struct {
	Type      string                     `json:"type"`
	Code      string                     `json:"code,omitempty"`
	ID        string                     `json:"id,omitempty"`
	Mode      string                     `json:"mode,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	ICE       []webrtc.ICEServer         `json:"ice,omitempty"`
	RelayOnly bool                       `json:"relayOnly,omitempty"`
	Error     string                     `json:"error,omitempty"`
}

func read(ctx context.Context, c *websocket.Conn) (message, error) {
	var msg message
	err := wsjson.Read(ctx, c, &msg)
	if err == nil && msg.Type == "error" {
		err = errors.New(msg.Error)
	}
	return msg, err
}

func write(ctx context.Context, c *websocket.Conn, msg message) error {
	return wsjson.Write(ctx, c, msg)
}

func (c Config) dial(ctx context.Context) (*websocket.Conn, error) {
	endpoint, err := c.endpoint()
	if err != nil {
		return nil, err
	}
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+c.Token)
	// Disable redirects so an enrollment key goes only to the configured endpoint.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	conn, res, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	if err != nil {
		if res != nil && res.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("跨网服务密钥不正确")
		}
		return nil, fmt.Errorf("无法连接跨网服务: %w", err)
	}
	conn.SetReadLimit(readLimit)
	return conn, nil
}

func open(ctx context.Context, cfg Config, code, action, expected string) (*websocket.Conn, message, error) {
	if !IsCode(code) {
		return nil, message{}, errors.New("设备码格式不正确")
	}
	conn, err := cfg.dial(ctx)
	if err != nil {
		return nil, message{}, err
	}
	if err := write(ctx, conn, message{Type: action, Code: NormalizeCode(code)}); err != nil {
		conn.CloseNow()
		return nil, message{}, err
	}
	msg, err := read(ctx, conn)
	if err == nil && msg.Type != expected {
		err = errors.New("跨网服务响应无效")
	}
	if err != nil {
		conn.CloseNow()
		return nil, message{}, err
	}
	return conn, msg, nil
}

func Register(ctx context.Context, cfg Config, code string) (*websocket.Conn, error) {
	conn, _, err := open(ctx, cfg, code, "register", "registered")
	return conn, err
}

type Offer struct {
	ID        string
	Mode      string
	SDP       webrtc.SessionDescription
	ICE       []webrtc.ICEServer
	RelayOnly bool
}

func ReadOffer(ctx context.Context, conn *websocket.Conn) (Offer, error) {
	msg, err := read(ctx, conn)
	if err != nil {
		return Offer{}, err
	}
	if msg.Type != "offer" || msg.ID == "" || msg.SDP == nil || msg.SDP.Type != webrtc.SDPTypeOffer {
		return Offer{}, errors.New("无效的跨网连接请求")
	}
	return Offer{ID: msg.ID, Mode: msg.Mode, SDP: *msg.SDP, ICE: msg.ICE, RelayOnly: msg.RelayOnly}, nil
}

func Reply(ctx context.Context, conn *websocket.Conn, id string, answer *webrtc.SessionDescription, reason string) error {
	return write(ctx, conn, message{Type: "answer", ID: id, SDP: answer, Error: reason})
}

type Guest struct {
	conn      *websocket.Conn
	ICE       []webrtc.ICEServer
	RelayOnly bool
}

func Join(ctx context.Context, cfg Config, code string) (*Guest, error) {
	conn, msg, err := open(ctx, cfg, code, "join", "ready")
	if err != nil {
		return nil, err
	}
	return &Guest{conn: conn, ICE: msg.ICE, RelayOnly: msg.RelayOnly}, nil
}

func (g *Guest) Close() { _ = g.conn.CloseNow() }

func (g *Guest) Exchange(ctx context.Context, mode string, offer *webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := write(ctx, g.conn, message{Type: "offer", Mode: mode, SDP: offer}); err != nil {
		return webrtc.SessionDescription{}, err
	}
	msg, err := read(ctx, g.conn)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if msg.Type != "answer" || msg.SDP == nil || msg.SDP.Type != webrtc.SDPTypeAnswer {
		return webrtc.SessionDescription{}, errors.New("无效的跨网连接应答")
	}
	return *msg.SDP, nil
}
