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
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.gearno.de/crypto/uuid"
	"go.gearno.de/kit/log"
	"go.gearno.de/kit/pg"
	"go.gearno.de/x/ref"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"go.probo.inc/probo/pkg/connector"
	"go.probo.inc/probo/pkg/coredata"
	"go.probo.inc/probo/pkg/gid"
	"go.probo.inc/probo/pkg/page"
	"go.probo.inc/probo/pkg/prosemirror"
	"go.probo.inc/probo/pkg/validator"
)

const (
	googleDocMimeType = "application/vnd.google-apps.document"
	pdfMimeType       = "application/pdf"
)

var (
	versionVRegex   = regexp.MustCompile(`(?i)\bv(\d+)\.(\d+)\b`)
	versionNumRegex = regexp.MustCompile(`(?i)(?:^|[^\d])(\d+)\.(\d+)(?:[^\d]|$)`)
)

func parseVersionFromFileName(fileName string) (int, int, bool) {
	if matches := versionVRegex.FindStringSubmatch(fileName); len(matches) == 3 {
		major, err1 := strconv.Atoi(matches[1])
		minor, err2 := strconv.Atoi(matches[2])
		if err1 == nil && err2 == nil {
			return major, minor, true
		}
	}

	base := fileName
	ext := filepath.Ext(fileName)
	if ext != "" {
		trimmedExt := strings.TrimPrefix(ext, ".")
		if _, err := strconv.Atoi(trimmedExt); err != nil {
			base = strings.TrimSuffix(fileName, ext)
		}
	}

	if matches := versionNumRegex.FindStringSubmatch(base); len(matches) == 3 {
		major, err1 := strconv.Atoi(matches[1])
		minor, err2 := strconv.Atoi(matches[2])
		if err1 == nil && err2 == nil {
			return major, minor, true
		}
	}

	return 0, 0, false
}

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

	var driveSvc *drive.Service

	switch conn := connRecord.Connection.(type) {
	case *connector.OAuth2Connection:
		if err := s.svc.connectorRegistry.ConfigureConnection(string(connRecord.Provider), conn); err != nil {
			return nil, nil, fmt.Errorf("cannot configure Google Drive connector: %w", err)
		}

		tokenBefore := conn.AccessToken

		var httpClient *http.Client

		refreshCfg := s.svc.connectorRegistry.GetOAuth2RefreshConfig(string(connRecord.Provider))
		if refreshCfg != nil {
			httpClient, err = conn.RefreshableClient(ctx, *refreshCfg)
		} else {
			httpClient, err = conn.Client(ctx)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("cannot create Google Drive HTTP client: %w", err)
		}

		if conn.AccessToken != tokenBefore {
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

		driveSvc, err = drive.NewService(ctx, option.WithHTTPClient(httpClient))
		if err != nil {
			return nil, nil, fmt.Errorf("cannot create Google Drive service client: %w", err)
		}

	case *connector.ServiceAccountConnection:
		if err := s.svc.connectorRegistry.ConfigureConnection(string(connRecord.Provider), conn); err != nil {
			return nil, nil, fmt.Errorf("cannot configure Google Drive connector: %w", err)
		}

		if conn.CredentialsJSON == "" {
			return nil, nil, fmt.Errorf("Google Drive service account credentials are empty")
		}

		driveSvc, err = drive.NewService(
			ctx,
			option.WithCredentialsJSON([]byte(conn.CredentialsJSON)),
			option.WithScopes(drive.DriveReadonlyScope),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot create Google Drive service client: %w", err)
		}

	default:
		return nil, nil, fmt.Errorf("unsupported connection protocol for Google Drive: %T", connRecord.Connection)
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
		OrderBy("modifiedTime desc").
		SupportsAllDrives(true).
		IncludeItemsFromAllDrives(true).
		Corpora("allDrives")

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
		pdfResp, err := driveSvc.Files.Get(fileID).SupportsAllDrives(true).Context(ctx).Download()
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

type googleDriveApprovalState struct {
	status             coredata.DocumentVersionStatus
	hasInProgress      bool
	inProgressApproval *drive.Approval
	latestApproved     *drive.Approval
	latestApprovedTime *time.Time
}

type googleDriveLinkMetadata struct {
	LastPublishedApprovalID   string     `json:"last_published_approval_id,omitempty"`
	LastPublishedApprovalTime *time.Time `json:"last_published_approval_time,omitempty"`
}

func parseGoogleDriveLinkMetadata(data json.RawMessage) googleDriveLinkMetadata {
	var meta googleDriveLinkMetadata
	if len(data) > 0 {
		_ = json.Unmarshal(data, &meta)
	}
	return meta
}

func serializeGoogleDriveLinkMetadata(meta googleDriveLinkMetadata) json.RawMessage {
	b, err := json.Marshal(meta)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func (s *GoogleDriveService) getDocumentApprovalState(
	ctx context.Context,
	driveSvc *drive.Service,
	fileID string,
) googleDriveApprovalState {
	approvalsCall := driveSvc.Approvals.List(fileID).
		Fields("items(approvalId,status,createTime,completeTime,modifyTime)")

	approvalList, err := approvalsCall.Context(ctx).Do()
	if err != nil {
		s.svc.logger.WarnCtx(
			ctx,
			"cannot list Google Drive approvals for file",
			log.Error(err),
			log.String("file_id", fileID),
		)
		return googleDriveApprovalState{
			status: coredata.DocumentVersionStatusDraft,
		}
	}

	if approvalList == nil || len(approvalList.Items) == 0 {
		return googleDriveApprovalState{
			status: coredata.DocumentVersionStatusDraft,
		}
	}

	var (
		latestApproved       *drive.Approval
		latestApprovedTime   time.Time
		latestInProgress     *drive.Approval
		latestInProgressTime time.Time
	)

	for _, item := range approvalList.Items {
		switch strings.ToUpper(item.Status) {
		case "APPROVED":
			t, _ := time.Parse(time.RFC3339, item.CompleteTime)
			if t.IsZero() {
				t, _ = time.Parse(time.RFC3339, item.ModifyTime)
			}
			if t.IsZero() {
				t, _ = time.Parse(time.RFC3339, item.CreateTime)
			}
			if latestApproved == nil || t.After(latestApprovedTime) {
				latestApproved = item
				latestApprovedTime = t
			}
		case "IN_PROGRESS":
			t, _ := time.Parse(time.RFC3339, item.CreateTime)
			if t.IsZero() {
				t, _ = time.Parse(time.RFC3339, item.ModifyTime)
			}
			if latestInProgress == nil || t.After(latestInProgressTime) {
				latestInProgress = item
				latestInProgressTime = t
			}
		}
	}

	res := googleDriveApprovalState{
		inProgressApproval: latestInProgress,
		latestApproved:     latestApproved,
	}
	if !latestApprovedTime.IsZero() {
		res.latestApprovedTime = &latestApprovedTime
	}

	if latestInProgress != nil {
		res.hasInProgress = true
		res.status = coredata.DocumentVersionStatusPendingApproval
		return res
	}

	if latestApproved != nil {
		res.status = coredata.DocumentVersionStatusPublished
		return res
	}

	res.status = coredata.DocumentVersionStatusDraft
	return res
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

	var (
		existingLink   coredata.DocumentExternalLink
		staleLinkFound bool
		hasActiveLink  bool
	)
	err = s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			if err := existingLink.LoadByExternalID(ctx, conn, scope, req.OrganizationID, coredata.ConnectorProviderGoogleDrive, req.FileID); err != nil {
				return err
			}
			var existingDoc coredata.Document
			if err := existingDoc.LoadByID(ctx, conn, scope, existingLink.DocumentID); err != nil {
				if errors.Is(err, coredata.ErrResourceNotFound) {
					staleLinkFound = true
					return nil
				}
				return err
			}
			hasActiveLink = true
			return nil
		},
	)
	if err == nil && hasActiveLink {
		return nil, nil, fmt.Errorf("google drive file is already linked: %w", coredata.ErrResourceAlreadyExists)
	}

	f, err := driveSvc.Files.Get(req.FileID).
		SupportsAllDrives(true).
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

	approvalState := s.getDocumentApprovalState(ctx, driveSvc, f.Id)

	var (
		major       int
		minor       int
		status      coredata.DocumentVersionStatus
		publishedAt *time.Time
		linkMeta    googleDriveLinkMetadata
	)

	fileMajor, fileMinor, hasVersionInName := parseVersionFromFileName(f.Name)

	switch {
	case approvalState.hasInProgress:
		status = coredata.DocumentVersionStatusPendingApproval
		if hasVersionInName {
			major, minor = fileMajor, fileMinor
		} else {
			major, minor = 1, 0
		}
		publishedAt = nil

	case approvalState.latestApproved != nil:
		status = coredata.DocumentVersionStatusPublished
		if hasVersionInName {
			major, minor = fileMajor, fileMinor
		} else {
			major, minor = 1, 0
		}
		if approvalState.latestApprovedTime != nil {
			publishedAt = approvalState.latestApprovedTime
		} else {
			publishedAt = &now
		}
		linkMeta.LastPublishedApprovalID = approvalState.latestApproved.ApprovalId
		linkMeta.LastPublishedApprovalTime = approvalState.latestApprovedTime

	default: // Draft
		status = coredata.DocumentVersionStatusDraft
		if hasVersionInName {
			major, minor = fileMajor, fileMinor
		} else {
			major, minor = 0, 1
		}
		publishedAt = nil
	}

	document := &coredata.Document{
		ID:             gid.New(scope.GetTenantID(), coredata.DocumentEntityType),
		OrganizationID: req.OrganizationID,
		Title:          title,
		WriteMode:      coredata.DocumentWriteModeGoogleDrive,
		Status:         coredata.DocumentStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if status == coredata.DocumentVersionStatusPublished {
		document.CurrentPublishedMajor = &major
		document.CurrentPublishedMinor = &minor
	}

	documentVersion := &coredata.DocumentVersion{
		ID:             gid.New(scope.GetTenantID(), coredata.DocumentVersionEntityType),
		DocumentID:     document.ID,
		OrganizationID: req.OrganizationID,
		Title:          title,
		DocumentType:   coredata.DocumentTypePolicy,
		Classification: coredata.DocumentClassificationInternal,
		Orientation:    coredata.DocumentVersionOrientationPortrait,
		Major:          major,
		Minor:          minor,
		Status:         status,
		Content:        contentJSON,
		FileID:         &fileRecord.ID,
		Changelog:      "Initial import from Google Drive",
		PublishedAt:    publishedAt,
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
		Metadata:       serializeGoogleDriveLinkMetadata(linkMeta),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	err = s.svc.pg.WithTx(
		ctx,
		func(ctx context.Context, tx pg.Tx) error {
			if staleLinkFound {
				if err := existingLink.Delete(ctx, tx, scope); err != nil {
					return fmt.Errorf("cannot delete stale external link: %w", err)
				}
			}
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
		SupportsAllDrives(true).
		Fields("id, name, mimeType, webViewLink, iconLink, modifiedTime").
		Context(ctx).
		Do()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot get Google Drive file metadata: %w", err)
	}

	var existingVersions coredata.DocumentVersions
	cursor := page.NewCursor(
		100,
		nil,
		page.Head,
		page.OrderBy[coredata.DocumentVersionOrderField]{
			Field:     coredata.DocumentVersionOrderFieldCreatedAt,
			Direction: page.OrderDirectionAsc,
		},
	)
	err = s.svc.pg.WithConn(
		ctx,
		func(ctx context.Context, conn pg.Querier) error {
			return existingVersions.LoadByDocumentID(ctx, conn, scope, document.ID, cursor, coredata.NewDocumentVersionFilter())
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot load existing document versions: %w", err)
	}

	var (
		activeNonPublished   *coredata.DocumentVersion
		lastPublishedVersion *coredata.DocumentVersion
		latestVersion        *coredata.DocumentVersion
	)

	for _, v := range existingVersions {
		if latestVersion == nil || v.CreatedAt.After(latestVersion.CreatedAt) {
			latestVersion = v
		}
		if v.Status == coredata.DocumentVersionStatusDraft || v.Status == coredata.DocumentVersionStatusPendingApproval {
			activeNonPublished = v
		}
		if v.Status == coredata.DocumentVersionStatusPublished {
			if lastPublishedVersion == nil || v.Major > lastPublishedVersion.Major || (v.Major == lastPublishedVersion.Major && v.Minor > lastPublishedVersion.Minor) {
				lastPublishedVersion = v
			}
		}
	}

	approvalState := s.getDocumentApprovalState(ctx, driveSvc, f.Id)
	linkMeta := parseGoogleDriveLinkMetadata(externalLink.Metadata)
	hasPublishedVersion := (document.CurrentPublishedMajor != nil && document.CurrentPublishedMinor != nil) || lastPublishedVersion != nil
	fileMajor, fileMinor, hasVersionInName := parseVersionFromFileName(f.Name)

	var (
		targetStatus       coredata.DocumentVersionStatus
		targetMajor        int
		targetMinor        int
		publishedAt        *time.Time
		shouldFetchContent bool
	)

	if approvalState.hasInProgress {
		// New version is currently in progress / awaiting approval in Google Drive.
		targetStatus = coredata.DocumentVersionStatusPendingApproval
		shouldFetchContent = true

		if hasVersionInName {
			targetMajor, targetMinor = fileMajor, fileMinor
		} else if hasPublishedVersion {
			targetMajor = *document.CurrentPublishedMajor
			targetMinor = *document.CurrentPublishedMinor + 1
		} else {
			targetMajor, targetMinor = 1, 0
		}

		if hasPublishedVersion && (targetMajor < *document.CurrentPublishedMajor || (targetMajor == *document.CurrentPublishedMajor && targetMinor <= *document.CurrentPublishedMinor)) {
			targetMajor = *document.CurrentPublishedMajor
			targetMinor = *document.CurrentPublishedMinor + 1
		}
	} else if hasPublishedVersion {
		// Document has a published version in Probo, and no approval is currently in progress.
		// Check whether a NEW approval was completed in Google Drive.
		isNewApproval := false
		if approvalState.latestApproved != nil {
			if linkMeta.LastPublishedApprovalID != "" {
				if approvalState.latestApproved.ApprovalId != linkMeta.LastPublishedApprovalID {
					isNewApproval = true
				}
			} else if lastPublishedVersion != nil {
				publishedTime := lastPublishedVersion.CreatedAt
				if lastPublishedVersion.PublishedAt != nil {
					publishedTime = *lastPublishedVersion.PublishedAt
				}
				if approvalState.latestApprovedTime != nil && approvalState.latestApprovedTime.After(publishedTime) {
					if hasVersionInName && (fileMajor > lastPublishedVersion.Major || (fileMajor == lastPublishedVersion.Major && fileMinor > lastPublishedVersion.Minor)) {
						isNewApproval = true
					}
				}
			}
		}

		if isNewApproval {
			targetStatus = coredata.DocumentVersionStatusPublished
			shouldFetchContent = true

			if hasVersionInName {
				targetMajor, targetMinor = fileMajor, fileMinor
			} else if activeNonPublished != nil {
				targetMajor, targetMinor = activeNonPublished.Major, activeNonPublished.Minor
			} else {
				targetMajor = *document.CurrentPublishedMajor
				targetMinor = *document.CurrentPublishedMinor + 1
			}

			if targetMajor < *document.CurrentPublishedMajor || (targetMajor == *document.CurrentPublishedMajor && targetMinor <= *document.CurrentPublishedMinor) {
				targetMajor = *document.CurrentPublishedMajor
				targetMinor = *document.CurrentPublishedMinor + 1
			}

			if approvalState.latestApprovedTime != nil {
				publishedAt = approvalState.latestApprovedTime
			} else {
				now := time.Now()
				publishedAt = &now
			}

			linkMeta.LastPublishedApprovalID = approvalState.latestApproved.ApprovalId
			linkMeta.LastPublishedApprovalTime = approvalState.latestApprovedTime
		} else {
			// No in-progress approval and no new completed approval in Google Drive.
			// Any modifications made in Google Docs are unapproved draft edits.
			// Do NOT overwrite the published version; retain the last approved version.
			shouldFetchContent = false

			if linkMeta.LastPublishedApprovalID == "" && approvalState.latestApproved != nil {
				linkMeta.LastPublishedApprovalID = approvalState.latestApproved.ApprovalId
				linkMeta.LastPublishedApprovalTime = approvalState.latestApprovedTime
			}
		}
	} else {
		// Document was never published in Probo.
		shouldFetchContent = true

		if approvalState.latestApproved != nil {
			targetStatus = coredata.DocumentVersionStatusPublished
			if hasVersionInName {
				targetMajor, targetMinor = fileMajor, fileMinor
			} else {
				targetMajor, targetMinor = 1, 0
			}
			if approvalState.latestApprovedTime != nil {
				publishedAt = approvalState.latestApprovedTime
			} else {
				now := time.Now()
				publishedAt = &now
			}
			linkMeta.LastPublishedApprovalID = approvalState.latestApproved.ApprovalId
			linkMeta.LastPublishedApprovalTime = approvalState.latestApprovedTime
		} else {
			targetStatus = coredata.DocumentVersionStatusDraft
			if hasVersionInName {
				targetMajor, targetMinor = fileMajor, fileMinor
			} else {
				targetMajor, targetMinor = 0, 1
			}
		}
	}

	if !shouldFetchContent {
		var resultVersion *coredata.DocumentVersion
		now := time.Now()

		err = s.svc.pg.WithTx(
			ctx,
			func(ctx context.Context, tx pg.Tx) error {
				if activeNonPublished != nil {
					if err := activeNonPublished.Delete(ctx, tx, scope); err != nil {
						return fmt.Errorf("cannot delete superseded active version: %w", err)
					}
				}

				externalLink.ExternalURL = f.WebViewLink
				externalLink.MimeType = &f.MimeType
				externalLink.LastSyncedAt = now
				externalLink.Metadata = serializeGoogleDriveLinkMetadata(linkMeta)
				externalLink.UpdatedAt = now
				if err := externalLink.Update(ctx, tx, scope); err != nil {
					return fmt.Errorf("cannot update external link: %w", err)
				}

				resultVersion = lastPublishedVersion
				if resultVersion == nil {
					resultVersion = latestVersion
				}
				return nil
			},
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("cannot sync document: %w", err)
		}

		return document, resultVersion, externalLink, nil
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

	var resultVersion *coredata.DocumentVersion
	err = s.svc.pg.WithTx(
		ctx,
		func(ctx context.Context, tx pg.Tx) error {
			if err := fileRecord.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot insert file record: %w", err)
			}

			var exactMatch *coredata.DocumentVersion
			for _, v := range existingVersions {
				if v.Major == targetMajor && v.Minor == targetMinor {
					exactMatch = v
					break
				}
			}

			orientation := coredata.DocumentVersionOrientationPortrait
			classification := coredata.DocumentClassificationInternal
			docType := coredata.DocumentTypePolicy
			if latestVersion != nil {
				if latestVersion.Orientation != "" {
					orientation = latestVersion.Orientation
				}
				if latestVersion.Classification != "" {
					classification = latestVersion.Classification
				}
				if latestVersion.DocumentType != "" {
					docType = latestVersion.DocumentType
				}
			}

			findAvailableMinor := func(major, minor int, excludeID gid.GID) int {
				used := make(map[int]bool)
				for _, v := range existingVersions {
					if (excludeID == (gid.GID{}) || v.ID != excludeID) && v.Major == major {
						used[v.Minor] = true
					}
				}
				for used[minor] {
					minor++
				}
				return minor
			}

			if targetStatus == coredata.DocumentVersionStatusPublished {
				if exactMatch != nil {
					exactMatch.Content = contentJSON
					exactMatch.FileID = &fileRecord.ID
					exactMatch.Status = coredata.DocumentVersionStatusPublished
					if exactMatch.PublishedAt == nil {
						exactMatch.PublishedAt = publishedAt
					}
					exactMatch.UpdatedAt = now
					if err := exactMatch.Update(ctx, tx, scope); err != nil {
						return fmt.Errorf("cannot update document version: %w", err)
					}
					resultVersion = exactMatch

					if activeNonPublished != nil && activeNonPublished.ID != exactMatch.ID {
						if err := activeNonPublished.Delete(ctx, tx, scope); err != nil {
							return fmt.Errorf("cannot delete superseded active version: %w", err)
						}
					}
				} else if activeNonPublished != nil {
					activeNonPublished.Major = targetMajor
					activeNonPublished.Minor = targetMinor
					activeNonPublished.Status = coredata.DocumentVersionStatusPublished
					activeNonPublished.PublishedAt = publishedAt
					activeNonPublished.Content = contentJSON
					activeNonPublished.FileID = &fileRecord.ID
					activeNonPublished.UpdatedAt = now
					if err := activeNonPublished.Update(ctx, tx, scope); err != nil {
						return fmt.Errorf("cannot update document version: %w", err)
					}
					resultVersion = activeNonPublished
				} else {
					newVersion := &coredata.DocumentVersion{
						ID:             gid.New(scope.GetTenantID(), coredata.DocumentVersionEntityType),
						DocumentID:     document.ID,
						OrganizationID: document.OrganizationID,
						Title:          document.Title,
						DocumentType:   docType,
						Classification: classification,
						Orientation:    orientation,
						Major:          targetMajor,
						Minor:          targetMinor,
						Status:         coredata.DocumentVersionStatusPublished,
						Content:        contentJSON,
						FileID:         &fileRecord.ID,
						Changelog:      "Synced from Google Drive",
						PublishedAt:    publishedAt,
						CreatedAt:      now,
						UpdatedAt:      now,
					}
					if err := newVersion.Insert(ctx, tx, scope); err != nil {
						return fmt.Errorf("cannot insert document version: %w", err)
					}
					resultVersion = newVersion
				}

				document.CurrentPublishedMajor = &targetMajor
				document.CurrentPublishedMinor = &targetMinor
				document.Status = coredata.DocumentStatusActive
				document.UpdatedAt = now
				if err := document.Update(ctx, tx, scope); err != nil {
					return fmt.Errorf("cannot update document: %w", err)
				}
			} else {
				// targetStatus is PENDING_APPROVAL or DRAFT
				if activeNonPublished != nil && (lastPublishedVersion == nil || activeNonPublished.CreatedAt.After(lastPublishedVersion.CreatedAt)) {
					targetMinor = findAvailableMinor(targetMajor, targetMinor, activeNonPublished.ID)
					activeNonPublished.Major = targetMajor
					activeNonPublished.Minor = targetMinor
					activeNonPublished.Status = targetStatus
					activeNonPublished.PublishedAt = nil
					activeNonPublished.Content = contentJSON
					activeNonPublished.FileID = &fileRecord.ID
					activeNonPublished.UpdatedAt = now
					if err := activeNonPublished.Update(ctx, tx, scope); err != nil {
						return fmt.Errorf("cannot update active document version: %w", err)
					}
					resultVersion = activeNonPublished
				} else {
					if activeNonPublished != nil {
						if err := activeNonPublished.Delete(ctx, tx, scope); err != nil {
							return fmt.Errorf("cannot delete superseded active version: %w", err)
						}
					}
					targetMinor = findAvailableMinor(targetMajor, targetMinor, gid.GID{})
					newVersion := &coredata.DocumentVersion{
						ID:             gid.New(scope.GetTenantID(), coredata.DocumentVersionEntityType),
						DocumentID:     document.ID,
						OrganizationID: document.OrganizationID,
						Title:          document.Title,
						DocumentType:   docType,
						Classification: classification,
						Orientation:    orientation,
						Major:          targetMajor,
						Minor:          targetMinor,
						Status:         targetStatus,
						Content:        contentJSON,
						FileID:         &fileRecord.ID,
						Changelog:      "Synced from Google Drive",
						PublishedAt:    nil,
						CreatedAt:      now,
						UpdatedAt:      now,
					}
					if err := newVersion.Insert(ctx, tx, scope); err != nil {
						return fmt.Errorf("cannot insert active document version: %w", err)
					}
					resultVersion = newVersion
				}
			}

			externalLink.ExternalURL = f.WebViewLink
			externalLink.MimeType = &f.MimeType
			externalLink.LastSyncedAt = now
			externalLink.Metadata = serializeGoogleDriveLinkMetadata(linkMeta)
			externalLink.UpdatedAt = now
			if err := externalLink.Update(ctx, tx, scope); err != nil {
				return fmt.Errorf("cannot update external link: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot persist synced Google Drive document: %w", err)
	}

	return document, resultVersion, externalLink, nil
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
