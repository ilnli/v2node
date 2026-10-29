package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/vmihailenco/msgpack/v5"
)

type OnlineUser struct {
	UID int
	IP  string
}

type UserInfo struct {
	Id          int    `json:"id" msgpack:"id"`
	Uuid        string `json:"uuid" msgpack:"uuid"`
	SpeedLimit  int    `json:"speed_limit" msgpack:"speed_limit"`
	DeviceLimit int    `json:"device_limit" msgpack:"device_limit"`
}

type UserListBody struct {
	Users []UserInfo `json:"users" msgpack:"users"`
}

type AliveMap struct {
	Alive map[int]int `json:"alive"`
}

// GetUserList will pull user from v2board
func (c *Client) GetUserList(ctx context.Context) ([]UserInfo, error) {
	const path = "/api/v1/server/UniProxy/user"
	r, err := c.client.R().
		SetContext(ctx).
		SetHeader("If-None-Match", c.userEtag).
		SetHeader("X-Response-Format", "msgpack").
		SetDoNotParseResponse(true).
		Get(path)
	if err != nil {
		return nil, err
	}
	if r == nil || r.RawResponse == nil {
		return nil, fmt.Errorf("received nil response or raw response")
	}
	defer r.RawResponse.Body.Close()

	if r.StatusCode() == 304 {
		return nil, nil
	}
	if r.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("user list HTTP status %d", r.StatusCode())
	}
	userlist := &UserListBody{Users: make([]UserInfo, 0)}
	if strings.Contains(r.Header().Get("Content-Type"), "application/x-msgpack") {
		decoder := msgpack.NewDecoder(r.RawResponse.Body)
		if err := decoder.Decode(userlist); err != nil {
			return nil, fmt.Errorf("decode user list error: %w", err)
		}
	} else {
		dec := jsontext.NewDecoder(r.RawResponse.Body)
		for {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, fmt.Errorf("decode user list error: %w", err)
			}
			if tok.Kind() == '"' && tok.String() == "users" {
				break
			}
		}
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, fmt.Errorf("decode user list error: %w", err)
		}
		if tok.Kind() != '[' {
			return nil, fmt.Errorf(`decode user list error: expected "users" array`)
		}
		for dec.PeekKind() != ']' {
			val, err := dec.ReadValue()
			if err != nil {
				return nil, fmt.Errorf("decode user list error: read user object: %w", err)
			}
			var u UserInfo
			if err := json.Unmarshal(val, &u); err != nil {
				return nil, fmt.Errorf("decode user list error: unmarshal user error: %w", err)
			}
			userlist.Users = append(userlist.Users, u)
		}
	}
	c.userEtag = r.Header().Get("ETag")
	if userlist.Users == nil {
		userlist.Users = make([]UserInfo, 0)
	}
	return userlist.Users, nil
}

// GetUserAlive will fetch the alive_ip count for users
func (c *Client) GetUserAlive(ctx context.Context) (map[int]int, error) {
	c.AliveMap = &AliveMap{}
	const path = "/api/v1/server/UniProxy/alivelist"
	r, err := c.client.R().
		SetContext(ctx).
		ForceContentType("application/json").
		Get(path)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		c.AliveMap.Alive = make(map[int]int)
		return c.AliveMap.Alive, nil
	}
	if r == nil || r.RawResponse == nil || r.StatusCode() >= 399 {
		c.AliveMap.Alive = make(map[int]int)
		return c.AliveMap.Alive, nil
	}
	defer r.RawResponse.Body.Close()
	if err := json.Unmarshal(r.Body(), c.AliveMap); err != nil {
		fmt.Printf("unmarshal user alive list error: %s", err)
		c.AliveMap.Alive = make(map[int]int)
	}

	return c.AliveMap.Alive, nil
}

type UserTraffic struct {
	UID      int
	Upload   int64
	Download int64
}

// ReportUserTraffic reports a delta. A supplied durable batch ID requires an
// explicit panel acknowledgement; retries always carry the same ID and payload.
func (c *Client) ReportUserTraffic(ctx context.Context, userTraffic []UserTraffic, batchIDs ...string) error {
	data := make(map[int][]int64, len(userTraffic))
	for _, traffic := range userTraffic {
		data[traffic.UID] = []int64{traffic.Upload, traffic.Download}
	}
	batchID := ""
	durable := len(batchIDs) > 0
	if durable {
		batchID = batchIDs[0]
	}
	if batchID == "" {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		batchID = hex.EncodeToString(token[:])
	}
	r, err := c.client.R().SetContext(ctx).SetHeader("X-Traffic-Batch-ID", batchID).
		SetBody(data).ForceContentType("application/json").Post("/api/v1/server/UniProxy/push")
	if err != nil {
		return err
	}
	if r == nil || r.StatusCode() < 200 || r.StatusCode() >= 300 {
		if r == nil {
			return fmt.Errorf("traffic report received no response")
		}
		return fmt.Errorf("traffic report HTTP status %d", r.StatusCode())
	}
	if durable {
		var acknowledgement struct {
			Data bool `json:"data"`
		}
		if err := json.Unmarshal(r.Body(), &acknowledgement); err != nil || !acknowledgement.Data {
			return fmt.Errorf("traffic report was not acknowledged by the panel")
		}
	}
	return nil
}

func (c *Client) ReportNodeOnlineUsers(ctx context.Context, data *map[int][]string) error {
	r, err := c.client.R().SetContext(ctx).SetBody(data).ForceContentType("application/json").Post("/api/v1/server/UniProxy/alive")
	if err != nil {
		return err
	}
	if r == nil || r.StatusCode() < 200 || r.StatusCode() >= 300 {
		if r == nil {
			return fmt.Errorf("online report received no response")
		}
		return fmt.Errorf("online report HTTP status %d", r.StatusCode())
	}
	return nil
}
