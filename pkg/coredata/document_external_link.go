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

package coredata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.gearno.de/kit/pg"
	"go.probo.inc/probo/pkg/gid"
)

type (
	DocumentExternalLink struct {
		DocumentID     gid.GID           `db:"document_id"`
		TenantID       string            `db:"tenant_id"`
		OrganizationID gid.GID           `db:"organization_id"`
		ConnectorID    gid.GID           `db:"connector_id"`
		Provider       ConnectorProvider `db:"provider"`
		ExternalID     string            `db:"external_id"`
		ExternalURL    string            `db:"external_url"`
		MimeType       *string           `db:"mime_type"`
		LastSyncedAt   time.Time         `db:"last_synced_at"`
		Metadata       json.RawMessage   `db:"metadata"`
		CreatedAt      time.Time         `db:"created_at"`
		UpdatedAt      time.Time         `db:"updated_at"`
	}

	DocumentExternalLinks []*DocumentExternalLink
)

func (l *DocumentExternalLink) LoadByDocumentID(
	ctx context.Context,
	conn pg.Querier,
	scope Scoper,
	documentID gid.GID,
) error {
	q := `
SELECT
    document_id,
    tenant_id,
    organization_id,
    connector_id,
    provider,
    external_id,
    external_url,
    mime_type,
    last_synced_at,
    metadata,
    created_at,
    updated_at
FROM
    document_external_links
WHERE
    %s
    AND document_id = @document_id
LIMIT 1;
`

	q = fmt.Sprintf(q, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"document_id": documentID}
	maps.Copy(args, scope.SQLArguments())

	return l.loadExactlyOne(ctx, conn, q, args)
}

func (l *DocumentExternalLink) LoadByExternalID(
	ctx context.Context,
	conn pg.Querier,
	scope Scoper,
	organizationID gid.GID,
	provider ConnectorProvider,
	externalID string,
) error {
	q := `
SELECT
    document_id,
    tenant_id,
    organization_id,
    connector_id,
    provider,
    external_id,
    external_url,
    mime_type,
    last_synced_at,
    metadata,
    created_at,
    updated_at
FROM
    document_external_links
WHERE
    %s
    AND organization_id = @organization_id
    AND provider = @provider
    AND external_id = @external_id
LIMIT 1;
`

	q = fmt.Sprintf(q, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"organization_id": organizationID,
		"provider":        provider,
		"external_id":     externalID,
	}
	maps.Copy(args, scope.SQLArguments())

	return l.loadExactlyOne(ctx, conn, q, args)
}

func (l *DocumentExternalLink) Insert(
	ctx context.Context,
	conn pg.Querier,
	scope Scoper,
) error {
	q := `
INSERT INTO document_external_links (
    document_id,
    tenant_id,
    organization_id,
    connector_id,
    provider,
    external_id,
    external_url,
    mime_type,
    last_synced_at,
    metadata,
    created_at,
    updated_at
) VALUES (
    @document_id,
    @tenant_id,
    @organization_id,
    @connector_id,
    @provider,
    @external_id,
    @external_url,
    @mime_type,
    @last_synced_at,
    @metadata,
    @created_at,
    @updated_at
);
`

	args := pgx.StrictNamedArgs{
		"document_id":    l.DocumentID,
		"tenant_id":      scope.GetTenantID(),
		"organization_id": l.OrganizationID,
		"connector_id":   l.ConnectorID,
		"provider":       l.Provider,
		"external_id":    l.ExternalID,
		"external_url":   l.ExternalURL,
		"mime_type":      l.MimeType,
		"last_synced_at": l.LastSyncedAt,
		"metadata":       l.Metadata,
		"created_at":     l.CreatedAt,
		"updated_at":     l.UpdatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			if pgErr.Code == "23505" {
				switch pgErr.ConstraintName {
				case "document_external_links_pkey",
					"document_external_links_organization_id_provider_external_id_key":
					return ErrResourceAlreadyExists
				}
			}
		}

		return fmt.Errorf("cannot insert document external link: %w", err)
	}

	return nil
}

func (l *DocumentExternalLink) Update(
	ctx context.Context,
	conn pg.Querier,
	scope Scoper,
) error {
	q := `
UPDATE document_external_links
SET
    external_id = @external_id,
    external_url = @external_url,
    mime_type = @mime_type,
    last_synced_at = @last_synced_at,
    metadata = @metadata,
    updated_at = @updated_at
WHERE
    %s
    AND document_id = @document_id;
`

	q = fmt.Sprintf(q, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"document_id":    l.DocumentID,
		"external_id":    l.ExternalID,
		"external_url":   l.ExternalURL,
		"mime_type":      l.MimeType,
		"last_synced_at": l.LastSyncedAt,
		"metadata":       l.Metadata,
		"updated_at":     l.UpdatedAt,
	}
	maps.Copy(args, scope.SQLArguments())

	result, err := conn.Exec(ctx, q, args)
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			if pgErr.Code == "23505" {
				return ErrResourceAlreadyExists
			}
		}

		return fmt.Errorf("cannot update document external link: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrResourceNotFound
	}

	return nil
}

func (l *DocumentExternalLink) Delete(
	ctx context.Context,
	conn pg.Querier,
	scope Scoper,
) error {
	q := `
DELETE FROM document_external_links
WHERE
    %s
    AND document_id = @document_id;
`

	q = fmt.Sprintf(q, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"document_id": l.DocumentID}
	maps.Copy(args, scope.SQLArguments())

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot delete document external link: %w", err)
	}

	return nil
}

func (l *DocumentExternalLink) loadExactlyOne(
	ctx context.Context,
	conn pg.Querier,
	q string,
	args pgx.StrictNamedArgs,
) error {
	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query document external link: %w", err)
	}

	link, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[DocumentExternalLink])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}

		return fmt.Errorf("cannot collect document external link: %w", err)
	}

	*l = link

	return nil
}
