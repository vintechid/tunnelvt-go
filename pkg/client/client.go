// Package client implements the tunnelvt client: username+password → JWT.
// JWT auto-refreshes when expired. User types credentials once.
package client

import (
	"bufio"
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
	"golang.org/x/term"

	"github.com/vintechid/tunnelvt-go/pkg/protocol"
)

const defaultServer = "https://gotunnel.vinstechid.com"

var BuildHash = "dev"

func identityFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tunnelvt.json")
}

type Client struct {
	App  string
	Port int

	username string
	password string
	jwt      string
	conn     *websocket.Conn
	writeMu  sync.Mutex
}

func New(app string, port int) *Client {
	return &Client{App: app, Port: port}
}

func (c *Client) Connect() error {
	for {
		if err := c.ensureJWT(); err != nil {
			return err
		}
		if err := c.tryConnect(); err != nil {
			if strings.Contains(err.Error(), "invalid or expired") {
				c.jwt = ""
				continue
			}
			return err
		}
		return nil
	}
}

func (c *Client) tryConnect() error {
	wsURL, _ := buildWSURL(defaultServer)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.conn = conn
	defer conn.Close()

	if err := c.writeJSON(protocol.Message{
		Type: protocol.TypeRegister, JWT: c.jwt, App: c.App, Port: c.Port,
		Version: "1.0.0", VHash: BuildHash,
	}); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	var ack protocol.Message
	if err := conn.ReadJSON(&ack); err != nil {
		return fmt.Errorf("ack: %w", err)
	}
	if ack.Type == protocol.TypeError {
		return fmt.Errorf("server rejected: %s", ack.Error)
	}

	log.Printf("[tunnelvt] connected — %s/%s -> localhost:%d", c.username, c.App, c.Port)
	fmt.Printf("https://gotunnel.vinstechid.com/%s/%s/\n", c.username, c.App)
	return c.forwardLoop()
}

func (c *Client) ensureJWT() error {
	data, err := os.ReadFile(identityFile())
	if err == nil {
		var creds struct {
			Username string `json:"username"`
			Password string `json:"password"`
			JWT      string `json:"jwt"`
		}
		if json.Unmarshal(data, &creds) == nil && creds.Username != "" {
			c.username = creds.Username
			c.password = creds.Password
			c.jwt = creds.JWT
		}
	}

	if c.username == "" {
		fmt.Print("Username: ")
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			return fmt.Errorf("no input")
		}
		c.username = strings.TrimSpace(sc.Text())
		if c.username == "" {
			return fmt.Errorf("username required")
		}
		fmt.Print("Password: ")
		p, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		c.password = strings.TrimSpace(string(p))
		if c.password == "" {
			return fmt.Errorf("password required")
		}
	}

	if c.jwt == "" {
		j, err := fetchJWT(defaultServer, c.username, c.password)
		if err != nil {
			os.Remove(identityFile())
			c.username = ""
			c.password = ""
			return fmt.Errorf("auth failed: %w", err)
		}
		c.jwt = j
	}

	creds := struct {
		Username string `json:"username"`
		Password string `json:"password"`
		JWT      string `json:"jwt"`
	}{c.username, c.password, c.jwt}
	b, _ := json.Marshal(creds)
	os.WriteFile(identityFile(), b, 0o600)
	return nil
}

func fetchJWT(serverURL, username, password string) (string, error) {
	body := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{username, password}
	b, _ := json.Marshal(body)
	resp, err := http.Post(serverURL+"/_tunnel/auth", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("%s", strings.TrimSpace(string(errBody)))
	}
	var out struct {
		JWT      string `json:"jwt"`
		Username string `json:"username"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.JWT, nil
}

func (c *Client) forwardLoop() error {
	httpClient := &http.Client{Timeout: 25 * time.Second}
	for {
		var msg protocol.Message
		if err := c.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				return fmt.Errorf("ws: %w", err)
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
	c.writeJSON(respMsg)
}

func (c *Client) doLocalRequest(httpClient *http.Client, msg protocol.Message) protocol.Message {
	errResp := func(e string) protocol.Message {
		return protocol.Message{Type: protocol.TypeError, ID: msg.ID, Error: e}
	}
	bodyBytes, _ := base64.StdEncoding.DecodeString(msg.Body)
	localURL := fmt.Sprintf("http://localhost:%d%s", c.Port, msg.Path)
	req, _ := http.NewRequest(msg.Method, localURL, strings.NewReader(string(bodyBytes)))
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

func buildWSURL(u string) (string, error) {
	p, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	if p.Scheme == "https" {
		p.Scheme = "wss"
	} else {
		p.Scheme = "ws"
	}
	p.Path = "/_tunnel/connect"
	p.RawQuery = ""
	p.Fragment = ""
	return p.String(), nil
}

func isHopByHop(h string) bool {
	switch strings.ToLower(h) {
	case "connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailers",
		"transfer-encoding", "upgrade":
		return true
	}
	return false
}
