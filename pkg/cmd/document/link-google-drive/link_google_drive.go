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

package linkgoogledrive

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"go.probo.inc/probo/pkg/cli/api"
	"go.probo.inc/probo/pkg/cmd/cmdutil"
)

const linkMutation = `
mutation($input: LinkGoogleDriveDocumentInput!) {
  linkGoogleDriveDocument(input: $input) {
    document {
      id
    }
  }
}
`

type linkResponse struct {
	LinkGoogleDriveDocument struct {
		Document struct {
			ID string `json:"id"`
		} `json:"document"`
	} `json:"linkGoogleDriveDocument"`
}

func NewCmdLinkGoogleDrive(f *cmdutil.Factory) *cobra.Command {
	var (
		flagOrg         string
		flagConnectorID string
		flagFileID      string
		flagTitle       string
	)

	cmd := &cobra.Command{
		Use:   "link-google-drive",
		Short: "Link a Google Drive file as a document",
		Example: `  # Link a Google Drive document
  prb document link-google-drive --connector-id <connector-id> --file-id <drive-file-id>

  # Link with custom title
  prb document link-google-drive --connector-id <connector-id> --file-id <drive-file-id> --title "My Doc"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if flagOrg == "" {
				flagOrg = hc.Organization
			}

			if flagOrg == "" {
				return fmt.Errorf("organization is required; pass --org or set a default with 'prb auth login'")
			}

			if flagConnectorID == "" {
				return fmt.Errorf("connector-id is required; pass --connector-id")
			}

			if flagFileID == "" {
				return fmt.Errorf("file-id is required; pass --file-id")
			}

			input := map[string]any{
				"organizationId": flagOrg,
				"connectorId":    flagConnectorID,
				"fileId":         flagFileID,
			}

			if flagTitle != "" {
				input["title"] = flagTitle
			}

			data, err := client.Do(
				linkMutation,
				map[string]any{"input": input},
			)
			if err != nil {
				return err
			}

			var resp linkResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				return fmt.Errorf("cannot parse response: %w", err)
			}

			_, _ = fmt.Fprintf(
				f.IOStreams.Out,
				"Linked Google Drive document %s\n",
				resp.LinkGoogleDriveDocument.Document.ID,
			)

			return nil
		},
	}

	cmd.Flags().StringVar(&flagOrg, "org", "", "Organization ID")
	cmd.Flags().StringVar(&flagConnectorID, "connector-id", "", "Google Drive connector ID")
	cmd.Flags().StringVar(&flagFileID, "file-id", "", "Google Drive file ID")
	cmd.Flags().StringVar(&flagTitle, "title", "", "Optional override document title")

	return cmd
}
