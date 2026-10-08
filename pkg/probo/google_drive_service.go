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

package probo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.gearno.de/crypto/uuid"
	"go.gearno.de/kit/pg"
	"go.gearno.de/x/ref"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"go.probo.inc/probo/pkg/connector"
	"go.probo.inc/probo/pkg/coredata"
	"go.probo.inc/probo/pkg/gid"
	"go.probo.inc/probo/pkg/prosemirror"
	"go.probo.inc/probo/pkg/validator"
)

const (
	googleDocMimeType = "application/vnd.google-apps.document"
	pdfMimeType       = "application/pdf"
)

type (
	GoogleDriveService struct {
		svc *Service
	}

	GoogleDriveFile struct {
		ID           string    `json:"id"`
		Name         string    `json:"name"`
		MimeType     string    `json:"mimeType"`
		WebViewLink  string    `json:"webViewLink"`
		IconLink     string    `json:"iconLink"`
		ModifiedTime time.Time `json:"modifiedTime"`
		Size         int64     `json:"size"`
	}

	GoogleDriveFileList struct {
		Files         []*GoogleDriveFile `json:"files"`
		NextPageToken string             `json:"nextPageToken"`
	}

	LinkGoogleDriveDocumentRequest struct {
		OrganizationID gid.GID `json:"organization_id"`
		ConnectorID    gid.GID `json:"connector_id"`
		FileID         string  `json:"file_id"`
		Title          *string `json:"title,omitempty"`
	}

	SyncGoogleDriveDocumentRequest struct {
		DocumentID gid.GID `json:"document_id"`
	}
)

func (req *LinkGoogleDriveDocumentRequest) Validate() error {
	v := validator.New()

	v.Check(req.OrganizationID, "organization_id", validator.Required(), validator.GID(coredata.OrganizationEntityType))
	v.Check(req.ConnectorID, "connector_id", validator.Required(), validator.GID(coredata.ConnectorEntityType))
	v.Check(req.FileID, "file_id", validator.Required(), validator.SafeTextNoNewLine(255))
	if req.Title != nil {
		v.Check(*req.Title, "title", validator.SafeTextNoNewLine(TitleMaxLength))
	}

	return v.Error()
}

func (req *SyncGoogleDriveDocumentRequest) Validate() error {
	v := validator.New()

	v.Check(req.DocumentID, "document_id", validator.Required(), validator.GID(coredata.DocumentEntityType))

	return v.Error()
}

func (s *GoogleDriveService) getDriveClient(
	ctx context.Context,
	scope coredata.Scoper,
	connectorID gid.GID,
) (*drive.Service, *coredata.Connector, error) {
	connRecord := &coredata.Connector{}
	err := s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			return connRecord.LoadByID(ctx, conn, scope, connectorID, s.svc.encryptionKey)
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot load connector: %w", err)
	}

	if connRecord.Provider != coredata.ConnectorProviderGoogleDrive {
		return nil, nil, fmt.Errorf("connector provider %q is not GOOGLE_DRIVE", connRecord.Provider)
	}

	oauthConn, ok := connRecord.Connection.(*connector.OAuth2Connection)
	if !ok {
		return nil, nil, fmt.Errorf("connector is not an OAuth2 connection")
	}

	if err := s.svc.connectorRegistry.ConfigureConnection(string(connRecord.Provider), oauthConn); err != nil {
		return nil, nil, fmt.Errorf("cannot configure Google Drive connector: %w", err)
	}

	tokenBefore := oauthConn.AccessToken

	var httpClient *http.Client

	refreshCfg := s.svc.connectorRegistry.GetOAuth2RefreshConfig(string(connRecord.Provider))
	if refreshCfg != nil {
		httpClient, err = oauthConn.RefreshableClient(ctx, *refreshCfg)
	} else {
		httpClient, err = oauthConn.Client(ctx)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create Google Drive HTTP client: %w", err)
	}

	if oauthConn.AccessToken != tokenBefore {
		connRecord.UpdatedAt = time.Now()
		err := s.svc.pg.WithTx(
			ctx,
			func(ctx context.Context, tx pg.Tx) error {
				if err := connRecord.Update(ctx, tx, scope, s.svc.encryptionKey); err != nil {
					return fmt.Errorf("cannot persist refreshed token: %w", err)
				}
				return nil
			},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot persist refreshed token: %w", err)
		}
	}

	driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create Google Drive service client: %w", err)
	}

	return driveSvc, connRecord, nil
}

func (s *GoogleDriveService) SearchFiles(
	ctx context.Context,
	scope coredata.Scoper,
	connectorID gid.GID,
	query *string,
	pageSize int64,
	pageToken *string,
) (*GoogleDriveFileList, error) {
	driveSvc, _, err := s.getDriveClient(ctx, scope, connectorID)
	if err != nil {
		return nil, err
	}

	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	qParts := []string{
		"trashed = false",
		fmt.Sprintf("(mimeType = '%s' or mimeType = '%s')", googleDocMimeType, pdfMimeType),
	}

	if query != nil && strings.TrimSpace(*query) != "" {
		cleaned := strings.ReplaceAll(strings.TrimSpace(*query), "'", "\\'")
		qParts = append(qParts, fmt.Sprintf("name contains '%s'", cleaned))
	}

	call := driveSvc.Files.List().
		Q(strings.Join(qParts, " and ")).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, iconLink, modifiedTime, size)").
		PageSize(pageSize).
		OrderBy("modifiedTime desc")

	if pageToken != nil && *pageToken != "" {
		call = call.PageToken(*pageToken)
	}

	fileList, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("cannot search Google Drive files: %w", err)
	}

	result := &GoogleDriveFileList{
		NextPageToken: fileList.NextPageToken,
		Files:         make([]*GoogleDriveFile, 0, len(fileList.Files)),
	}

	for _, f := range fileList.Files {
		var modTime time.Time
		if f.ModifiedTime != "" {
			modTime, _ = time.Parse(time.RFC3339, f.ModifiedTime)
		}

		result.Files = append(result.Files, &GoogleDriveFile{
			ID:           f.Id,
			Name:         f.Name,
			MimeType:     f.MimeType,
			WebViewLink:  f.WebViewLink,
			IconLink:     f.IconLink,
			ModifiedTime: modTime,
			Size:         f.Size,
		})
	}

	return result, nil
}

func (s *GoogleDriveService) fetchFileContentAndPDF(
	ctx context.Context,
	driveSvc *drive.Service,
	fileID string,
	mimeType string,
) (string, []byte, error) {
	var contentJSON string
	var pdfBytes []byte

	switch mimeType {
	case googleDocMimeType:
		// Export HTML for in-app viewing
		htmlResp, err := driveSvc.Files.Export(fileID, "text/html").Context(ctx).Download()
		if err != nil {
			return "", nil, fmt.Errorf("cannot export Google Doc HTML: %w", err)
		}
		defer htmlResp.Body.Close()

		htmlBody, err := io.ReadAll(htmlResp.Body)
		if err != nil {
			return "", nil, fmt.Errorf("cannot read Google Doc HTML: %w", err)
		}

		pmDoc, err := prosemirror.ParseHTML(string(htmlBody))
		if err != nil {
			// Fallback: simple document with link if HTML conversion encounters unexpected structure
			pmDoc = prosemirror.Node{
				Type: prosemirror.NodeDoc,
				Content: []prosemirror.Node{
					{
						Type: prosemirror.NodeParagraph,
						Content: []prosemirror.Node{
							{
								Type: prosemirror.NodeText,
								Text: ref.Ref("Google Document content synced from Google Drive."),
							},
						},
					},
				},
			}
		}

		encoded, err := json.Marshal(pmDoc)
		if err != nil {
			return "", nil, fmt.Errorf("cannot marshal ProseMirror doc: %w", err)
		}
		contentJSON = string(encoded)

		// Export PDF for Trust Portal watermarking & publication
		pdfResp, err := driveSvc.Files.Export(fileID, pdfMimeType).Context(ctx).Download()
		if err != nil {
			return "", nil, fmt.Errorf("cannot export Google Doc PDF: %w", err)
		}
		defer pdfResp.Body.Close()

		pdfBytes, err = io.ReadAll(pdfResp.Body)
		if err != nil {
			return "", nil, fmt.Errorf("cannot read Google Doc PDF export: %w", err)
		}

	case pdfMimeType:
		// Direct PDF download
		pdfResp, err := driveSvc.Files.Get(fileID).Context(ctx).Download()
		if err != nil {
			return "", nil, fmt.Errorf("cannot download Google Drive PDF: %w", err)
		}
		defer pdfResp.Body.Close()

		pdfBytes, err = io.ReadAll(pdfResp.Body)
		if err != nil {
			return "", nil, fmt.Errorf("cannot read Google Drive PDF data: %w", err)
		}

		// Empty/minimal content for pure PDF
		pmDoc := prosemirror.Node{
			Type: prosemirror.NodeDoc,
			Content: []prosemirror.Node{
				{
					Type: prosemirror.NodeParagraph,
					Content: []prosemirror.Node{
						{
							Type: prosemirror.NodeText,
							Text: ref.Ref("PDF document linked from Google Drive."),
						},
					},
				},
			},
		}
		encoded, _ := json.Marshal(pmDoc)
		contentJSON = string(encoded)

	default:
		return "", nil, fmt.Errorf("unsupported mime type: %s", mimeType)
	}

	return contentJSON, pdfBytes, nil
}

func (s *GoogleDriveService) LinkDocument(
	ctx context.Context,
	scope coredata.Scoper,
	req LinkGoogleDriveDocumentRequest,
) (*coredata.Document, *coredata.DocumentExternalLink, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}

	driveSvc, _, err := s.getDriveClient(ctx, scope, req.ConnectorID)
	if err != nil {
		return nil, nil, err
	}

	var existingLink coredata.DocumentExternalLink
	err = s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			return existingLink.LoadByExternalID(ctx, conn, scope, req.OrganizationID, coredata.ConnectorProviderGoogleDrive, req.FileID)
		},
	)
	if err == nil {
		return nil, nil, fmt.Errorf("google drive file is already linked: %w", coredata.ErrResourceAlreadyExists)
	}

	f, err := driveSvc.Files.Get(req.FileID).
		Fields("id, name, mimeType, webViewLink, iconLink, modifiedTime").
		Context(ctx).
		Do()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot get Google Drive file metadata: %w", err)
	}

	title := f.Name
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = strings.TrimSpace(*req.Title)
	}

	contentJSON, pdfBytes, err := s.fetchFileContentAndPDF(ctx, driveSvc, f.Id, f.MimeType)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot fetch file content: %w", err)
	}

	now := time.Now()

	fileRecord := &coredata.File{
		ID:             gid.New(scope.GetTenantID(), coredata.FileEntityType),
		OrganizationID: req.OrganizationID,
		BucketName:     s.svc.bucket,
		MimeType:       pdfMimeType,
		FileName:       fmt.Sprintf("%s.pdf", title),
		FileKey:        uuid.MustNewV4().String(),
		Visibility:     coredata.FileVisibilityPrivate,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	fileSize, err := s.svc.fileManager.PutFile(
		ctx,
		fileRecord,
		bytes.NewReader(pdfBytes),
		map[string]string{
			"type":                 "google-drive-document",
			"google-drive-file-id": f.Id,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot upload document PDF to storage: %w", err)
	}
	fileRecord.FileSize = fileSize

	document := &coredata.Document{
		ID:                    gid.New(scope.GetTenantID(), coredata.DocumentEntityType),
		OrganizationID:        req.OrganizationID,
		CurrentPublishedMajor: ref.Ref(1),
		CurrentPublishedMinor: ref.Ref(0),
		WriteMode:             coredata.DocumentWriteModeGoogleDrive,
		Status:                coredata.DocumentStatusActive,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	documentVersion := &coredata.DocumentVersion{
		ID:             gid.New(scope.GetTenantID(), coredata.DocumentVersionEntityType),
		DocumentID:     document.ID,
		OrganizationID: req.OrganizationID,
		Title:          title,
		DocumentType:   coredata.DocumentTypePolicy,
		Classification: coredata.DocumentClassificationInternal,
		Major:          1,
		Minor:          0,
		Status:         coredata.DocumentVersionStatusPublished,
		Content:        contentJSON,
		FileID:         &fileRecord.ID,
		Changelog:      "Initial import from Google Drive",
		PublishedAt:    &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	externalLink := &coredata.DocumentExternalLink{
		DocumentID:     document.ID,
		TenantID:       scope.GetTenantID().String(),
		OrganizationID: req.OrganizationID,
		ConnectorID:    req.ConnectorID,
		Provider:       coredata.ConnectorProviderGoogleDrive,
		ExternalID:     f.Id,
		ExternalURL:    f.WebViewLink,
		MimeType:       &f.MimeType,
		LastSyncedAt:   now,
		Metadata:       json.RawMessage("{}"),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	err = s.svc.pg.WithTx(
		ctx,
		func(ctx context.Context, tx pg.Tx) error {
			if err := fileRecord.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot insert file record: %w", err)
			}
			if err := document.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot insert document: %w", err)
			}
			if err := documentVersion.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot insert document version: %w", err)
			}
			if err := externalLink.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot insert document external link: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot persist linked Google Drive document: %w", err)
	}

	return document, externalLink, nil
}

func (s *GoogleDriveService) SyncDocument(
	ctx context.Context,
	scope coredata.Scoper,
	req SyncGoogleDriveDocumentRequest,
) (*coredata.Document, *coredata.DocumentVersion, *coredata.DocumentExternalLink, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, nil, err
	}

	document := &coredata.Document{}
	err := s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			return document.LoadByID(ctx, conn, scope, req.DocumentID)
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot load document: %w", err)
	}

	externalLink := &coredata.DocumentExternalLink{}
	err = s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			return externalLink.LoadByDocumentID(ctx, conn, scope, req.DocumentID)
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot load document external link: %w", err)
	}

	driveSvc, _, err := s.getDriveClient(ctx, scope, externalLink.ConnectorID)
	if err != nil {
		return nil, nil, nil, err
	}

	f, err := driveSvc.Files.Get(externalLink.ExternalID).
		Fields("id, name, mimeType, webViewLink, iconLink, modifiedTime").
		Context(ctx).
		Do()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot get Google Drive file metadata: %w", err)
	}

	contentJSON, pdfBytes, err := s.fetchFileContentAndPDF(ctx, driveSvc, f.Id, f.MimeType)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot fetch file content: %w", err)
	}

	now := time.Now()

	fileRecord := &coredata.File{
		ID:             gid.New(scope.GetTenantID(), coredata.FileEntityType),
		OrganizationID: document.OrganizationID,
		BucketName:     s.svc.bucket,
		MimeType:       pdfMimeType,
		FileName:       fmt.Sprintf("%s.pdf", document.Title),
		FileKey:        uuid.MustNewV4().String(),
		Visibility:     coredata.FileVisibilityPrivate,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	fileSize, err := s.svc.fileManager.PutFile(
		ctx,
		fileRecord,
		bytes.NewReader(pdfBytes),
		map[string]string{
			"type":                 "google-drive-document",
			"google-drive-file-id": f.Id,
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot upload synced document PDF to storage: %w", err)
	}
	fileRecord.FileSize = fileSize

	// Load latest published version to increment minor version
	latestVersion := &coredata.DocumentVersion{}
	err = s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			return latestVersion.LoadLatestPublishedVersion(ctx, conn, scope, document.ID)
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot load latest document version: %w", err)
	}

	newVersion := &coredata.DocumentVersion{
		ID:             gid.New(scope.GetTenantID(), coredata.DocumentVersionEntityType),
		DocumentID:     document.ID,
		OrganizationID: document.OrganizationID,
		Title:          document.Title,
		DocumentType:   latestVersion.DocumentType,
		Classification: latestVersion.Classification,
		Major:          latestVersion.Major,
		Minor:          latestVersion.Minor + 1,
		Status:         coredata.DocumentVersionStatusPublished,
		Content:        contentJSON,
		FileID:         &fileRecord.ID,
		Changelog:      "Synced from Google Drive",
		PublishedAt:    &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	externalLink.ExternalURL = f.WebViewLink
	externalLink.MimeType = &f.MimeType
	externalLink.LastSyncedAt = now
	externalLink.UpdatedAt = now

	document.CurrentPublishedMinor = &newVersion.Minor
	document.UpdatedAt = now

	err = s.svc.pg.WithTx(
		ctx,
		func(ctx context.Context, tx pg.Tx) error {
			if err := fileRecord.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot insert file record: %w", err)
			}
			if err := newVersion.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot insert document version: %w", err)
			}
			if err := document.Update(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot update document: %w", err)
			}
			if err := externalLink.Update(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot update external link: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot persist synced Google Drive document: %w", err)
	}

	return document, newVersion, externalLink, nil
}

func (s *GoogleDriveService) GetExternalLink(
	ctx context.Context,
	scope coredata.Scoper,
	documentID gid.GID,
) (*coredata.DocumentExternalLink, error) {
	link := &coredata.DocumentExternalLink{}
	err := s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			return link.LoadByDocumentID(ctx, conn, scope, documentID)
		},
	)
	if err != nil {
		return nil, err
	}
	return link, nil
}
