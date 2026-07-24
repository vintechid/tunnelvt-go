# tunnelvt — Go

Simple tunneling server + client in Go. No login. Expose local apps at `domain/<device>/<app>`.

## Install

```bash
go install github.com/vintechids/tunnelvt-go/cmd/tunnelvtd@latest
go install github.com/vintechids/tunnelvt-go/cmd/tunnelvt@latest
```

## Usage

### Server

```bash
tunnelvtd -addr :8080
```

### Client

```bash
tunnelvt -server wss://tunnel.example.com -app myapp -port 3000
```

Output:
```
[tunnelvt] connected — a1b2c3d4e5f6g7h8/myapp -> localhost:3000
```

Your app is now at:

```
https://tunnel.example.com/a1b2c3d4e5f6g7h8/myapp/
```

## Protocol

See `pkg/protocol/message.go` for the full JSON-over-WebSocket message schema.

## License

MIT
