// Package protocol defines shared message types and utilities for tunnelvt.
package protocol

import (
	"crypto/rand"
	"encoding/hex"
)

type MessageType string

const (
	TypeRegister MessageType = "register"
	TypeRequest  MessageType = "request"
	TypeResponse MessageType = "response"
	TypeError    MessageType = "error"
)

type Message struct {
	Type    MessageType       `json:"type"`
	ID      string            `json:"id,omitempty"`
	JWT     string            `json:"jwt,omitempty"`
	Version string            `json:"version,omitempty"`  // client version
	VHash   string            `json:"vhash,omitempty"`    // build hash (ldflags)
	Device  string            `json:"device,omitempty"`
	App     string            `json:"app,omitempty"`
	Port    int               `json:"port,omitempty"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Status  int               `json:"status,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func GenerateDeviceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
