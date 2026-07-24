// Package client implements the tunnelvt client: connect to server via WebSocket,
// register a device+app mapping, and forward incoming requests to a local port.
//
// Designed to work through Cloudflare — WebSocket upgrade happens over HTTP.
// Server IP stays hidden behind the CDN.
package client

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/vintechids/tunnelvt-go/pkg/protocol"
)

// Client holds the tunnel client configuration.
type Client struct {
	ServerURL string // e.g. "https://tunnel.example.com"
	Token     string // pre-shared auth token
	Device    string // pre-set device ID; random if empty
	App       string // app name for this tunnel
	Port      int    // local port to expose

	conn    *websocket.Conn
	writeMu sync.Mutex
}

// New creates a Client. Device can be empty (randomly assigned).
func New(serverURL, token, device, app string, port int) *Client {
	return &Client{
		ServerURL: serverURL,
		Token:     token,
		Device:    device,
		App:       app,
		Port:      port,
	}
}

// Connect dials the server, registers the tunnel, and blocks forwarding
// requests until the connection drops.
func (c *Client) Connect() error {
	wsURL, err := buildWSURL(c.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}
	c.conn = conn
	defer conn.Close()

	// Register.
	device := c.Device
	if device == "" {
		device = protocol.GenerateDeviceID()
	}

	if err := c.writeJSON(protocol.Message{
		Type:   protocol.TypeRegister,
		Token:  c.Token,
		Device: device,
		App:    c.App,
		Port:   c.Port,
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

	c.Device = ack.Device
	log.Printf("[tunnelvt] connected — %s/%s -> localhost:%d", c.Device, c.App, c.Port)

	return c.forwardLoop()
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
		Type:    protocol.TypeResponse,
		ID:      msg.ID,
		Status:  resp.StatusCode,
		Headers: respHeaders,
		Body:    base64.StdEncoding.EncodeToString(respBody),
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
