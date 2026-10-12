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

package connector_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.probo.inc/probo/pkg/connector"
)

func TestServiceAccountConnection_RoundTrip(t *testing.T) {
	t.Parallel()

	input := &connector.ServiceAccountConnection{
		FolderID: "folder-123",
	}

	raw, err := json.Marshal(input)
	require.NoError(t, err)

	got, err := connector.UnmarshalConnection(
		string(connector.ProtocolServiceAccount),
		"GOOGLE_DRIVE",
		raw,
	)
	require.NoError(t, err)

	conn, ok := got.(*connector.ServiceAccountConnection)
	require.True(t, ok, "expected a *ServiceAccountConnection")
	assert.Equal(t, connector.ProtocolServiceAccount, conn.Type())
	assert.Equal(t, "folder-123", conn.FolderID)
	assert.NotEmpty(t, conn.Scopes())
}

func TestServiceAccountConnection_StoresNoSecret(t *testing.T) {
	t.Parallel()

	conn := &connector.ServiceAccountConnection{
		CredentialsJSON: `{"private_key": "secret"}`,
		FolderID:        "folder-abc",
	}

	raw, err := json.Marshal(conn)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	assert.Equal(t, map[string]any{
		"type":      "SERVICE_ACCOUNT",
		"folder_id": "folder-abc",
	}, decoded)
	assert.NotContains(t, decoded, "credentials_json")
	assert.NotContains(t, decoded, "private_key")
}

func TestServiceAccountConnector_ConfigureConnection(t *testing.T) {
	t.Parallel()

	c := &connector.ServiceAccountConnector{
		CredentialsJSON: `{"type": "service_account"}`,
		FolderID:        "default-folder",
	}

	conn := &connector.ServiceAccountConnection{}
	err := c.ConfigureConnection(conn)
	require.NoError(t, err)
	assert.Equal(t, `{"type": "service_account"}`, conn.CredentialsJSON)
	assert.Equal(t, "default-folder", conn.FolderID)
}

func TestServiceAccountConnection_Capabilities(t *testing.T) {
	t.Parallel()

	conn := &connector.ServiceAccountConnection{}
	assert.False(t, connector.SupportsOrganizationPicker(conn))
	assert.False(t, connector.SupportsScopeGrantCheck(conn))
	assert.False(t, connector.SupportsReconnect(conn))

	probe := connector.CapabilityProbe(connector.ProtocolServiceAccount)
	require.NotNil(t, probe)
	assert.Equal(t, connector.ProtocolServiceAccount, probe.Type())
}
