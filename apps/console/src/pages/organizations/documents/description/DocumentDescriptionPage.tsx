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

import { ArrowSquareOutIcon, ArrowsClockwiseIcon } from "@phosphor-icons/react";
import { formatError } from "@probo/helpers";
import { dateFormat } from "@probo/i18n";
import { Badge, Button, GoogleLogo, RichEditor, useToast } from "@probo/ui";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { type PreloadedQuery, useMutation, usePreloadedQuery } from "react-relay";
import { useOutletContext } from "react-router";
import { graphql } from "relay-runtime";
import { useDebounceCallback } from "usehooks-ts";

import type { DocumentDescriptionPage_syncMutation } from "#/__generated__/core/DocumentDescriptionPage_syncMutation.graphql";
import type { DocumentDescriptionPage_updateContentMutation } from "#/__generated__/core/DocumentDescriptionPage_updateContentMutation.graphql";
import type { DocumentDescriptionPageQuery } from "#/__generated__/core/DocumentDescriptionPageQuery.graphql";

const autoSaveIntervalMs = 1000;

export const documentDescriptionPageQuery = graphql`
  query DocumentDescriptionPageQuery($documentId: ID! $versionId: ID! $versionSpecified: Boolean!) {
    # We use this on /documents/:documentId/versions/:versionId/description
    version: node(id: $versionId) @include(if: $versionSpecified) {
      __typename
      ... on DocumentVersion {
        id
        content
        status
      }
    }
    document: node(id: $documentId) {
      __typename
      ... on Document {
        id
        status
        writeMode
        canUpdate: permission(action: "core:document:update")
        externalLink {
          id
          externalId
          externalUrl
          lastSyncedAt
        }
        # We use this on /documents/:documentId/description
        lastVersion: versions(first: 1 orderBy: { field: CREATED_AT, direction: DESC }) @skip(if: $versionSpecified) {
          edges {
            node {
              id
              content
              status
            }
          }
        }
      }
    }
  }
`;

const updateContentMutation = graphql`
  mutation DocumentDescriptionPage_updateContentMutation($input: UpdateDocumentInput!) {
    updateDocument(input: $input) {
      document {
        id
      }
      documentVersion {
        id
        content
        status
      }
    }
  }
`;

const syncGoogleDriveDocumentMutation = graphql`
  mutation DocumentDescriptionPage_syncMutation($input: SyncGoogleDriveDocumentInput!) {
    syncGoogleDriveDocument(input: $input) {
      document {
        id
        status
        writeMode
        externalLink {
          id
          externalId
          externalUrl
          lastSyncedAt
        }
        lastVersion: versions(first: 1 orderBy: { field: CREATED_AT, direction: DESC }) {
          edges {
            node {
              id
              content
              status
            }
          }
        }
      }
    }
  }
`;

export function DocumentDescriptionPage(props: {
  queryRef: PreloadedQuery<DocumentDescriptionPageQuery>;
  versionChangedAt: number;
}) {
  const { queryRef, versionChangedAt } = props;

  const { t } = useTranslation();
  const { toast } = useToast();
  const { onDocumentUpdated, isEditable } = useOutletContext<{
    onDocumentUpdated: () => void;
    isEditable: boolean;
  }>();

  const { document, version } = usePreloadedQuery<DocumentDescriptionPageQuery>(
    documentDescriptionPageQuery,
    queryRef,
  );
  if (document.__typename !== "Document" || (version && version.__typename !== "DocumentVersion")) {
    throw new Error("invalid type for node");
  }

  const lastVersion = document.lastVersion?.edges[0].node;
  const currentVersion = lastVersion ?? version;
  if (!currentVersion) {
    throw new Error("Document version not found");
  }

  const [updateContent] = useMutation<DocumentDescriptionPage_updateContentMutation>(updateContentMutation);
  const [syncGoogleDriveDocument, isSyncing] = useMutation<DocumentDescriptionPage_syncMutation>(
    syncGoogleDriveDocumentMutation,
  );

  const documentId = document.id;
  const wasDraft = currentVersion.status === "DRAFT";

  const handleUpdate = useDebounceCallback(
    useCallback((content: string) => {
      updateContent({
        variables: {
          input: {
            id: documentId,
            content,
          },
        },
        onCompleted: (data, errors) => {
          if (errors?.length) {
            toast({
              title: t("documentDescriptionPage.errors.title"),
              description: formatError(t("documentDescriptionPage.errors.save"), errors),
              variant: "error",
            });
            return;
          }

          const draftReturned = !!data.updateDocument.documentVersion;
          if (wasDraft !== draftReturned) {
            onDocumentUpdated();
          }

          toast({
            title: t("documentDescriptionPage.messages.successTitle"),
            description: t("documentDescriptionPage.messages.saved"),
            variant: "success",
          });
        },
        onError: (error) => {
          toast({
            title: t("documentDescriptionPage.errors.title"),
            description: error.message ?? t("documentDescriptionPage.errors.save"),
            variant: "error",
          });
        },
      });
    }, [documentId, wasDraft, updateContent, toast, t, onDocumentUpdated]),
    autoSaveIntervalMs,
  );

  const handleSync = () => {
    syncGoogleDriveDocument({
      variables: {
        input: {
          documentId: document.id,
        },
      },
      onCompleted(_, errors) {
        if (errors?.length) {
          toast({
            title: t("documentDescriptionPage.errors.title"),
            description: formatError(t("documentDescriptionPage.googleDrive.syncError"), errors),
            variant: "error",
          });
          return;
        }
        toast({
          title: t("documentDescriptionPage.messages.successTitle"),
          description: t("documentDescriptionPage.googleDrive.syncSuccess"),
          variant: "success",
        });
        onDocumentUpdated();
      },
      onError(error) {
        toast({
          title: t("documentDescriptionPage.errors.title"),
          description: error.message,
          variant: "error",
        });
      },
    });
  };

  const isGoogleDrive = document.writeMode === "GOOGLE_DRIVE";

  const canEdit = isEditable
    && document.canUpdate
    && document.status !== "ARCHIVED"
    && document.writeMode !== "GENERATED"
    && !isGoogleDrive;

  // The editor key must change on explicit actions (delete draft, edit
  // title/type) but NOT on auto-save side effects (cursor preservation).
  // We track a "data generation" that only increments when an explicit
  // action (versionChangedAt change) is followed by fresh data arriving
  // (currentVersion.id change). This uses React's "adjust state during
  // render" pattern so we avoid refs-during-render and setState-in-effects.
  const [prevVCA, setPrevVCA] = useState(versionChangedAt);
  const [prevVersionId, setPrevVersionId] = useState(currentVersion.id);
  const [dataGeneration, setDataGeneration] = useState(0);
  const [pendingExplicit, setPendingExplicit] = useState(false);

  if (versionChangedAt !== prevVCA) {
    setPrevVCA(versionChangedAt);
    if (currentVersion.id !== prevVersionId) {
      // Both changed at once — data was already available.
      setPrevVersionId(currentVersion.id);
      setDataGeneration(g => g + 1);
      setPendingExplicit(false);
    } else {
      // Explicit action fired but data hasn't arrived yet.
      setPendingExplicit(true);
    }
  } else if (currentVersion.id !== prevVersionId) {
    setPrevVersionId(currentVersion.id);
    if (pendingExplicit) {
      // Fresh data arrived for a pending explicit action — remount.
      setDataGeneration(g => g + 1);
      setPendingExplicit(false);
    }
    // Otherwise auto-save changed the version — don't bump generation.
  }

  const editorKey = `${version?.id ?? document.id}-${dataGeneration}`;

  return (
    <div className="flex flex-col flex-1">
      {isGoogleDrive && (
        <div className="flex items-center justify-between px-4 py-3 mb-4 rounded-lg bg-bg-secondary border border-border-primary">
          <div className="flex items-center gap-3">
            <GoogleLogo className="size-5 shrink-0" />
            <div>
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium text-txt-primary">
                  {t("documentDescriptionPage.googleDrive.syncedWithDrive")}
                </span>
                <Badge variant="neutral" size="sm">
                  {t("documentDescriptionPage.googleDrive.readOnly")}
                </Badge>
              </div>
              {document.externalLink?.lastSyncedAt && (
                <p className="text-xs text-txt-secondary">
                  {t("documentDescriptionPage.googleDrive.lastSynced", {
                    time: dateFormat(new Date(document.externalLink.lastSyncedAt)),
                  })}
                </p>
              )}
            </div>
          </div>
          <div className="flex items-center gap-2">
            {document.externalLink?.externalUrl && (
              <Button
                variant="secondary"
                size="sm"
                icon={ArrowSquareOutIcon}
                asChild
              >
                <a
                  href={document.externalLink.externalUrl}
                  target="_blank"
                  rel="noreferrer"
                >
                  {t("documentDescriptionPage.googleDrive.openInDrive")}
                </a>
              </Button>
            )}
            {document.canUpdate && (
              <Button
                variant="secondary"
                size="sm"
                icon={ArrowsClockwiseIcon}
                onClick={handleSync}
                loading={isSyncing}
              >
                {t("documentDescriptionPage.googleDrive.syncNow")}
              </Button>
            )}
          </div>
        </div>
      )}
      <RichEditor
        key={editorKey}
        className="flex-1"
        content={currentVersion.content}
        data-theme="document"
        disabled={!canEdit}
        onChangeContent={handleUpdate}
      />
    </div>
  );
}
