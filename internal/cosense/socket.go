package cosense

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/coder/websocket"
)

var safeErrorName = regexp.MustCompile(`^[A-Za-z]{1,48}Error$`)

// Diagnostics must never contain packet bodies, page data, or credentials.
func writeDiagnostic(stage, detail string) {
	log.Printf("cosense write: stage=%s detail=%s", stage, detail)
}

func packetType(packet string) string {
	if len(packet) == 0 || packet[0] < '0' || packet[0] > '6' {
		return "unknown"
	}
	if packet[0] == '4' && len(packet) > 1 && packet[1] >= '0' && packet[1] <= '6' {
		return packet[:2]
	}
	return packet[:1]
}

// A deliberately narrow Engine.IO v4 / Socket.IO default-namespace client.
// One connection, one acknowledged commit event, no reconnect/replay.
func (c *Client) connect(ctx context.Context) (*websocket.Conn, error) {
	head := http.Header{}
	head.Set("Cookie", (&http.Cookie{Name: "connect.sid", Value: c.sid}).String())
	head.Set("Origin", c.baseURL)
	head.Set("Referer", c.baseURL+"/")
	ws, res, err := websocket.Dial(ctx, c.baseURL+"/socket.io/?EIO=4&transport=websocket", &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: head})
	if err != nil {
		if res != nil && (res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 429) {
			return nil, &APIError{Status: res.StatusCode}
		}
		return nil, errors.New("Cosense WebSocket connection failed")
	}
	ws.SetReadLimit(maxResponseBytes)
	fail := func() (*websocket.Conn, error) {
		ws.CloseNow()
		return nil, errors.New("unsupported Cosense Socket.IO handshake")
	}
	packet, err := readPacket(ctx, ws)
	if err != nil || !strings.HasPrefix(packet, "0{") {
		return fail()
	}
	var hello struct {
		SID string `json:"sid"`
	}
	if json.Unmarshal([]byte(packet[1:]), &hello) != nil || hello.SID == "" {
		return fail()
	}
	if ws.Write(ctx, websocket.MessageText, []byte(`40{"protocolVersion":2}`)) != nil {
		return fail()
	}
	packet, err = readPacket(ctx, ws)
	if err != nil || !(packet == "40" || strings.HasPrefix(packet, "40{")) {
		return fail()
	}
	return ws, nil
}

func readPacket(ctx context.Context, ws *websocket.Conn) (string, error) {
	for {
		kind, b, err := ws.Read(ctx)
		if err != nil {
			return "", err
		}
		if kind != websocket.MessageText {
			return "", errors.New("unexpected binary packet")
		}
		if string(b) == "2" {
			if err := ws.Write(ctx, websocket.MessageText, []byte("3")); err != nil {
				return "", err
			}
			continue
		}
		return string(b), nil
	}
}

func sendCommit(ctx context.Context, ws *websocket.Conn, data commit) (string, error) {
	b, err := json.Marshal([]any{"commit", data})
	if err != nil {
		return "", errors.New("cannot encode commit")
	}
	if ws.Write(ctx, websocket.MessageText, append([]byte("421"), b...)) != nil {
		writeDiagnostic("commit_send", "write_failed")
		return "", ErrUncertain
	}
	for {
		packet, err := readPacket(ctx, ws)
		if err != nil {
			writeDiagnostic("commit_ack", "read_failed")
			return "", ErrUncertain
		}
		if strings.HasPrefix(packet, "42") {
			continue
		} // Unsolicited events are not acknowledgements.
		if !strings.HasPrefix(packet, "431[") {
			writeDiagnostic("commit_ack", "unexpected_packet_type_"+packetType(packet))
			return "", ErrUncertain
		}
		var ack []struct {
			Data struct {
				CommitID string `json:"commitId"`
			} `json:"data"`
			Error *struct {
				Name string `json:"name"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(packet[3:]), &ack) != nil || len(ack) != 1 {
			writeDiagnostic("commit_ack", "invalid_ack")
			return "", ErrUncertain
		}
		if ack[0].Error != nil {
			name := ack[0].Error.Name
			if !safeErrorName.MatchString(name) {
				name = "unknown"
			}
			writeDiagnostic("commit_ack", "server_error_"+name)
			switch ack[0].Error.Name {
			case "NotFastForwardError":
				return "", ErrConflict
			case "DuplicateTitleError":
				return "", ErrExists
			case "NotLoggedInError", "NotMemberError", "ForbiddenError":
				return "", &APIError{Status: 403}
			default:
				return "", ErrUncertain
			}
		}
		if !objectID.MatchString(ack[0].Data.CommitID) {
			writeDiagnostic("commit_ack", "missing_or_invalid_commit_id")
			return "", ErrUncertain
		}
		return ack[0].Data.CommitID, nil
	}
}
