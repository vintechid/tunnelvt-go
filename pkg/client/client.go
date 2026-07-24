// Package client implements the tunnelvt client: auto-fetch JWT from server
// on first run, persist to ~/.tunnelvt.json, use on subsequent connects.
//
// Trust-on-first-use identity — no login, no shared token needed.
package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/vintechid/tunnelvt-go/pkg/protocol"
)

const defaultServer = "https://gotunnel.vinstechid.com"

func identityFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tunnelvt.json")
}

// BuildHash is set at compile time via ldflags:
//
//	go build -ldflags "-X github.com/vintechid/tunnelvt-go/pkg/client.BuildHash=abc123" ./cmd/tunnelvt
var BuildHash = "dev"

type Client struct {
	App  string
	Port int

	device  string
	jwt     string
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func New(app string, port int) *Client {
	return &Client{App: app, Port: port}
}

func (c *Client) Connect() error {
	if err := c.loadOrFetchIdentity(); err != nil {
		return fmt.Errorf("identity: %w", err)
	}

	wsURL, err := buildWSURL(defaultServer)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}
	c.conn = conn
	defer conn.Close()

	if err := c.writeJSON(protocol.Message{
		Type: protocol.TypeRegister, JWT: c.jwt, Device: c.device, App: c.App, Port: c.Port,
		Version: "1.0.0", VHash: BuildHash,
	}); err != nil {
		return fmt.Errorf("register write error: %w", err)
	}

	var ack protocol.Message
	if err := conn.ReadJSON(&ack); err != nil {
		return fmt.Errorf("register ack error: %w", err)
	}
	if ack.Type == protocol.TypeError {
		return fmt.Errorf("server rejected registration: %s", ack.Error)
	}

	c.device = ack.Device
	log.Printf("[tunnelvt] connected — %s/%s -> localhost:%d", c.device, c.App, c.Port)
	fmt.Printf("https://gotunnel.vinstechid.com/%s/%s/\n", c.device, c.App)

	return c.forwardLoop()
}

func (c *Client) loadOrFetchIdentity() error {
	data, err := os.ReadFile(identityFile())
	if err == nil {
		var id struct {
			Device string `json:"device"`
			JWT    string `json:"jwt"`
		}
		if json.Unmarshal(data, &id) == nil && id.JWT != "" {
			c.device = id.Device
			c.jwt = id.JWT
			return nil
		}
	}

	c.device = protocol.GenerateDeviceID()
	jwtStr, err := fetchJWT(defaultServer, c.device)
	if err != nil {
		return fmt.Errorf("fetch JWT: %w", err)
	}
	c.jwt = jwtStr

	id := struct {
		Device string `json:"device"`
		JWT    string `json:"jwt"`
	}{c.device, c.jwt}
	b, _ := json.Marshal(id)
	os.WriteFile(identityFile(), b, 0o600)

	return nil
}

func fetchJWT(serverURL, device string) (string, error) {
	body := struct{ Device string }{Device: device}
	b, _ := json.Marshal(body)
	resp, err := http.Post(serverURL+"/_tunnel/hello", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	var out struct {
		JWT    string `json:"jwt"`
		Device string `json:"device"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.JWT, nil
}

func (c *Client) forwardLoop() error {
	httpClient := &http.Client{Timeout: 25 * time.Second}
	for {
		var msg protocol.Message
		if err := c.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				return fmt.Errorf("ws read error: %w", err)
			}
			return nil
		}
		if msg.Type == protocol.TypeRequest {
			go c.handleRequest(httpClient, msg)
		}
	}
}

func (c *Client) handleRequest(httpClient *http.Client, msg protocol.Message) {
	respMsg := c.doLocalRequest(httpClient, msg)
	if err := c.writeJSON(respMsg); err != nil {
		log.Printf("[tunnelvt] write response error: %v", err)
	}
}

func (c *Client) doLocalRequest(httpClient *http.Client, msg protocol.Message) protocol.Message {
	errResp := func(errStr string) protocol.Message {
		return protocol.Message{Type: protocol.TypeError, ID: msg.ID, Error: errStr}
	}
	bodyBytes, err := base64.StdEncoding.DecodeString(msg.Body)
	if err != nil {
		return errResp("invalid body encoding")
	}
	localURL := fmt.Sprintf("http://localhost:%d%s", c.Port, msg.Path)
	req, err := http.NewRequest(msg.Method, localURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return errResp(fmt.Sprintf("create local request: %v", err))
	}
	for k, v := range msg.Headers {
		if !isHopByHop(k) {
			req.Header.Set(k, v)
		}
	}
	if host, ok := msg.Headers["Host"]; ok && host != "" {
		req.Host = host
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return errResp(fmt.Sprintf("local request failed: %v", err))
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}
	return protocol.Message{
		Type: protocol.TypeResponse, ID: msg.ID, Status: resp.StatusCode,
		Headers: respHeaders, Body: base64.StdEncoding.EncodeToString(respBody),
	}
}

func (c *Client) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(v)
}

func buildWSURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/_tunnel/connect"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func isHopByHop(header string) bool {
	switch strings.ToLower(header) {
	case "connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailers",
		"transfer-encoding", "upgrade":
		return true
	}
	return false
}
