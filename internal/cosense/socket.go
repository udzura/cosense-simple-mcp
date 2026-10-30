package cosense

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// A deliberately narrow Engine.IO v4 / Socket.IO default-namespace client.
// One connection, one acknowledged socket.io-request commit, no reconnect/replay.
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
	if ws.Write(ctx, websocket.MessageText, []byte("40")) != nil {
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
	b, err := json.Marshal([]any{"socket.io-request", map[string]any{"method": "commit", "data": data}})
	if err != nil {
		return "", errors.New("cannot encode commit")
	}
	if ws.Write(ctx, websocket.MessageText, append([]byte("421"), b...)) != nil {
		return "", ErrUncertain
	}
	for {
		packet, err := readPacket(ctx, ws)
		if err != nil {
			return "", ErrUncertain
		}
		if strings.HasPrefix(packet, "42") {
			continue
		} // Unsolicited events are not acknowledgements.
		if !strings.HasPrefix(packet, "431[") {
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
			return "", ErrUncertain
		}
		if ack[0].Error != nil {
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
			return "", ErrUncertain
		}
		return ack[0].Data.CommitID, nil
	}
}
