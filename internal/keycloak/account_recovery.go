package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) ClientID() string { return c.cid }

// The password grant requests offline_access. LogoutUser removes online SSO
// sessions, but offline grants must be deleted explicitly for each configured
// application client. Keep the user profile untouched.
func (a *AdminClient) InvalidateUserSessions(ctx context.Context, realm, userID string, clientIDs []string) error {
	if err := a.LogoutUser(ctx, realm, userID); err != nil {
		return err
	}
	token, err := a.AdminToken(ctx)
	if err != nil {
		return err
	}
	request := func(method, target string, out any) error {
		req, err := http.NewRequestWithContext(ctx, method, target, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := a.http.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if method == http.MethodDelete && resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.StatusCode >= 300 {
			return &RequestError{Operation: "invalidate offline session", StatusCode: resp.StatusCode}
		}
		if out != nil {
			return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
		}
		return nil
	}
	root := fmt.Sprintf("%s/admin/realms/%s", a.base, url.PathEscape(realm))
	seenClients := map[string]bool{}
	seenSessions := map[string]bool{}
	for _, clientID := range clientIDs {
		clientID = strings.TrimSpace(clientID)
		if clientID == "" || seenClients[clientID] {
			continue
		}
		seenClients[clientID] = true
		var clients []clientRep
		if err := request(http.MethodGet, root+"/clients?clientId="+url.QueryEscape(clientID), &clients); err != nil {
			return err
		}
		for _, client := range clients {
			var sessions []userSessionRep
			if err := request(http.MethodGet, root+"/users/"+url.PathEscape(userID)+"/offline-sessions/"+url.PathEscape(client.ID), &sessions); err != nil {
				return err
			}
			for _, session := range sessions {
				if seenSessions[session.ID] {
					continue
				}
				seenSessions[session.ID] = true
				if err := request(http.MethodDelete, root+"/sessions/"+url.PathEscape(session.ID)+"?isOffline=true", nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
