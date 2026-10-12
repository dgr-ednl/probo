// Copyright (c) 2026 Probo Inc <hello@probo.com>.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"go.probo.inc/probo/pkg/gid"
)

type (
	ServiceAccountConnector struct {
		CredentialsJSON string
		FolderID        string
	}

	ServiceAccountConnection struct {
		CredentialsJSON string `json:"credentials_json,omitempty"`
		FolderID        string `json:"folder_id,omitempty"`
	}
)

const (
	ProtocolServiceAccount ProtocolType = "SERVICE_ACCOUNT"
)

var (
	_ Connector                    = (*ServiceAccountConnector)(nil)
	_ ConnectionConfigurer         = (*ServiceAccountConnector)(nil)
	_ Connection                   = (*ServiceAccountConnection)(nil)
	_ HTTPConnection               = (*ServiceAccountConnection)(nil)
	_ RuntimeConfigurationRequired = (*ServiceAccountConnection)(nil)
	_ OrganizationSelector         = (*ServiceAccountConnection)(nil)
	_ ScopeGranter                 = (*ServiceAccountConnection)(nil)
	_ Reconnector                  = (*ServiceAccountConnection)(nil)
)

func (c *ServiceAccountConnector) ConfigureConnection(conn Connection) error {
	saConn, ok := conn.(*ServiceAccountConnection)
	if !ok {
		return fmt.Errorf("service account runtime configuration requires a service account connection")
	}

	saConn.CredentialsJSON = c.CredentialsJSON
	if saConn.FolderID == "" {
		saConn.FolderID = c.FolderID
	}

	return nil
}

func (c *ServiceAccountConnector) Initiate(
	ctx context.Context,
	provider string,
	organizationID gid.GID,
	opts InitiateOptions,
	r *http.Request,
) (string, error) {
	continueURL := r.URL.Query().Get("continue")
	if continueURL == "" {
		continueURL = r.URL.Query().Get("continue_url")
	}
	if continueURL == "" {
		continueURL = "/"
	}
	return continueURL, nil
}

func (c *ServiceAccountConnector) Complete(
	ctx context.Context,
	r *http.Request,
) (Connection, *gid.GID, string, error) {
	return &ServiceAccountConnection{
		CredentialsJSON: c.CredentialsJSON,
		FolderID:        c.FolderID,
	}, nil, "", nil
}

func (c ServiceAccountConnection) MarshalJSON() ([]byte, error) {
	return json.Marshal(
		&struct {
			Type     string `json:"type"`
			FolderID string `json:"folder_id,omitempty"`
		}{
			Type:     string(ProtocolServiceAccount),
			FolderID: c.FolderID,
		},
	)
}

func (c *ServiceAccountConnection) UnmarshalJSON(data []byte) error {
	var s struct {
		FolderID string `json:"folder_id,omitempty"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	c.FolderID = s.FolderID
	return nil
}

func (c *ServiceAccountConnection) Type() ProtocolType {
	return ProtocolServiceAccount
}

func (c *ServiceAccountConnection) Scopes() []string {
	return []string{"https://www.googleapis.com/auth/drive.readonly"}
}

func (c *ServiceAccountConnection) RequiresRuntimeConfiguration() {}

func (c *ServiceAccountConnection) Client(ctx context.Context) (*http.Client, error) {
	if c.CredentialsJSON == "" {
		return nil, fmt.Errorf("service account credentials not configured")
	}

	creds, err := google.CredentialsFromJSON(
		ctx,
		[]byte(c.CredentialsJSON),
		"https://www.googleapis.com/auth/drive.readonly",
	)
	if err != nil {
		return nil, fmt.Errorf("cannot parse service account credentials: %w", err)
	}

	return oauth2.NewClient(ctx, creds.TokenSource), nil
}

func (c *ServiceAccountConnection) SupportsOrganizationPicker() bool { return false }
func (c *ServiceAccountConnection) SupportsScopeGrantCheck() bool    { return false }
func (c *ServiceAccountConnection) SupportsReconnect() bool          { return false }
