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

package syncgoogledrive

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"go.probo.inc/probo/pkg/cli/api"
	"go.probo.inc/probo/pkg/cmd/cmdutil"
)

const syncMutation = `
mutation($input: SyncGoogleDriveDocumentInput!) {
  syncGoogleDriveDocument(input: $input) {
    document {
      id
    }
  }
}
`

type syncResponse struct {
	SyncGoogleDriveDocument struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	} `json:"syncGoogleDriveDocument"`
}

func NewCmdSyncGoogleDrive(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-google-drive <document-id>",
		Short: "Sync a Google Drive linked document with latest content and PDF",
		Example: `  # Sync a document from Google Drive
  prb document sync-google-drive doc_123456`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			documentID := args[0]

			cfg, err := f.Config()
			if err != nil {
				return err
			}

			host, hc, err := cfg.DefaultHost()
			if err != nil {
				return err
			}

			client := api.NewClient(
				host,
				hc.Token,
				"/api/console/v1/graphql",
				cfg.HTTPTimeoutDuration(),
			)

			data, err := client.Do(
				syncMutation,
				map[string]any{
					"input": map[string]any{
						"documentId": documentID,
					},
				},
			)
			if err != nil {
				return err
			}

			var resp syncResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("cannot parse response: %w", err)
			}

			_, _ = fmt.Fprintf(
				f.IOStreams.Out,
				"Synced Google Drive document %s\n",
				resp.SyncGoogleDriveDocument.Document.ID,
			)

			return nil
		},
	}

	return cmd
}
