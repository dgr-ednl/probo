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

import { ArrowSquareOutIcon, FileIcon, FilePdfIcon, FileTextIcon, MagnifyingGlassIcon } from "@phosphor-icons/react";
import { formatError } from "@probo/helpers";
import { dateFormat } from "@probo/i18n";
import {
  Badge,
  Breadcrumb,
  Button,
  Dialog,
  DialogContent,
  DialogFooter,
  GoogleLogo,
  IconCheckmark1,
  Input,
  Label,
  Option,
  PropertyRow,
  Select,
  Spinner,
  useDialogRef,
  useToast,
} from "@probo/ui";
import { Suspense, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useLazyLoadQuery, useMutation } from "react-relay";
import { useNavigate } from "react-router";
import { graphql } from "relay-runtime";
import { useDebounceCallback } from "usehooks-ts";

import type { LinkGoogleDriveDocumentDialogConnectorsQuery } from "#/__generated__/core/LinkGoogleDriveDocumentDialogConnectorsQuery.graphql";
import type { LinkGoogleDriveDocumentDialogFilesQuery } from "#/__generated__/core/LinkGoogleDriveDocumentDialogFilesQuery.graphql";
import type { LinkGoogleDriveDocumentDialogMutation } from "#/__generated__/core/LinkGoogleDriveDocumentDialogMutation.graphql";
import { ControlledField } from "#/components/form/ControlledField";
import { DocumentClassificationOptions } from "#/components/form/DocumentClassificationOptions";
import { DocumentTypeOptions } from "#/components/form/DocumentTypeOptions";
import { PeopleMultiSelectField } from "#/components/form/PeopleMultiSelectField";
import { useFormWithSchema } from "#/hooks/useFormWithSchema";
import { useOrganizationId } from "#/hooks/useOrganizationId";
import { z } from "#/lib/zod";

type LinkGoogleDriveDocumentDialogProps = {
  trigger?: ReactNode;
  connection: string;
};

const connectorsQuery = graphql`
  query LinkGoogleDriveDocumentDialogConnectorsQuery($organizationId: ID!) {
    organization: node(id: $organizationId) {
      __typename
      ... on Organization {
        id
        connectors {
          id
          provider
          displayName
        }
      }
    }
  }
`;

const filesQuery = graphql`
  query LinkGoogleDriveDocumentDialogFilesQuery($connectorId: ID!, $query: String) {
    googleDriveFiles(connectorId: $connectorId, query: $query) {
      files {
        id
        name
        mimeType
        webViewLink
        iconLink
        modifiedTime
        size
      }
    }
  }
`;

const linkGoogleDriveDocumentMutation = graphql`
  mutation LinkGoogleDriveDocumentDialogMutation(
    $input: LinkGoogleDriveDocumentInput!
    $connections: [ID!]!
  ) {
    linkGoogleDriveDocument(input: $input) {
      documentEdge @prependEdge(connections: $connections) {
        node {
          id
          canUpdate: permission(action: "core:document:update")
          canDelete: permission(action: "core:document:delete")
          canRequestSignatures: permission(action: "core:document-version:request-signature")
          canArchive: permission(action: "core:document:archive")
          canUnarchive: permission(action: "core:document:unarchive")
          ...DocumentListItemFragment
        }
      }
      document {
        id
      }
    }
  }
`;

type GoogleDriveFileItem = {
  id: string;
  name: string;
  mimeType: string;
  webViewLink: string;
  iconLink?: string | null;
  modifiedTime?: string | null;
  size?: number | string | null;
};

export function LinkGoogleDriveDocumentDialog({ trigger, connection }: LinkGoogleDriveDocumentDialogProps) {
  const { t } = useTranslation();
  const dialogRef = useDialogRef();

  return (
    <Dialog
      ref={dialogRef}
      trigger={trigger}
      title={(
        <Breadcrumb
          items={[
            t("linkGoogleDriveDocumentDialog.breadcrumbs.documents"),
            t("linkGoogleDriveDocumentDialog.breadcrumbs.linkGoogleDrive"),
          ]}
        />
      )}
    >
      <Suspense
        fallback={(
          <div className="flex items-center justify-center p-12">
            <Spinner />
          </div>
        )}
      >
        <LinkGoogleDriveContent
          connection={connection}
          onClose={() => dialogRef.current?.close()}
        />
      </Suspense>
    </Dialog>
  );
}

function LinkGoogleDriveContent({
  connection,
  onClose,
}: {
  connection: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const organizationId = useOrganizationId();
  const navigate = useNavigate();

  const data = useLazyLoadQuery<LinkGoogleDriveDocumentDialogConnectorsQuery>(
    connectorsQuery,
    { organizationId },
    { fetchPolicy: "network-only" },
  );

  const org = data.organization?.__typename === "Organization" ? data.organization : null;
  const googleDriveConnectors = org?.connectors?.filter(c => c.provider === "GOOGLE_DRIVE") ?? [];

  if (googleDriveConnectors.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-12 text-center space-y-4">
        <div className="p-4 rounded-full bg-subtle">
          <GoogleLogo className="size-10" />
        </div>
        <div className="space-y-1 max-w-sm">
          <h3 className="text-base font-medium text-txt-primary">
            {t("linkGoogleDriveDocumentDialog.notConnected.title")}
          </h3>
          <p className="text-sm text-txt-secondary">
            {t("linkGoogleDriveDocumentDialog.notConnected.description")}
          </p>
        </div>
        <Button
          variant="primary"
          onClick={() => {
            window.location.href = `/api/console/v1/connectors/initiate?provider=GOOGLE_DRIVE&organization_id=${organizationId}&continue_url=${encodeURIComponent(window.location.href)}`;
          }}
        >
          {t("linkGoogleDriveDocumentDialog.notConnected.connectAction")}
        </Button>
      </div>
    );
  }

  return (
    <LinkGoogleDriveForm
      googleDriveConnectors={googleDriveConnectors}
      organizationId={organizationId}
      connection={connection}
      onClose={onClose}
      onSuccess={(newDocId) => {
        onClose();
        if (newDocId) {
          navigate(`/organizations/${organizationId}/governance/documents/${newDocId}/description`);
        }
      }}
    />
  );
}

function LinkGoogleDriveForm({
  googleDriveConnectors,
  organizationId,
  connection,
  onClose,
  onSuccess,
}: {
  googleDriveConnectors: Array<{ id: string; displayName: string }>;
  organizationId: string;
  connection: string;
  onClose: () => void;
  onSuccess: (id?: string) => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const [selectedConnectorId, setSelectedConnectorId] = useState(googleDriveConnectors[0].id);
  const [searchQuery, setSearchQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [selectedFile, setSelectedFile] = useState<GoogleDriveFileItem | null>(null);

  const handleSearchChange = useDebounceCallback((value: string) => {
    setDebouncedQuery(value);
  }, 400);

  const documentSchema = z.object({
    title: z.string().min(1, t("linkGoogleDriveDocumentDialog.validation.titleRequired")),
    documentType: z.enum([
      "OTHER",
      "GOVERNANCE",
      "POLICY",
      "PROCEDURE",
      "PLAN",
      "REGISTER",
      "RECORD",
      "REPORT",
      "TEMPLATE",
    ]),
    classification: z.enum(["PUBLIC", "INTERNAL", "CONFIDENTIAL", "SECRET"]),
    defaultApproverIds: z.array(z.string()),
  });

  const { control, handleSubmit, register, formState, setValue } = useFormWithSchema(
    documentSchema,
    {
      defaultValues: {
        title: "",
        documentType: "POLICY",
        classification: "INTERNAL",
        defaultApproverIds: [],
      },
    },
  );

  const errors = formState.errors ?? {};
  const [linkGoogleDriveDocument, isLinking]
    = useMutation<LinkGoogleDriveDocumentDialogMutation>(linkGoogleDriveDocumentMutation);

  const handleSelectFile = (file: GoogleDriveFileItem) => {
    setSelectedFile(file);
    setValue("title", file.name, { shouldValidate: true });
  };

  const onSubmit = (formData: z.infer<typeof documentSchema>) => {
    if (!selectedFile) {
      toast({
        title: t("linkGoogleDriveDocumentDialog.errors.title"),
        description: t("linkGoogleDriveDocumentDialog.validation.fileRequired"),
        variant: "error",
      });
      return;
    }

    linkGoogleDriveDocument({
      variables: {
        input: {
          organizationId,
          connectorId: selectedConnectorId,
          fileId: selectedFile.id,
          title: formData.title,
        },
        connections: [connection],
      },
      onCompleted(response, mutationErrors) {
        if (mutationErrors?.length) {
          toast({
            title: t("linkGoogleDriveDocumentDialog.errors.title"),
            description: formatError(t("linkGoogleDriveDocumentDialog.errors.link"), mutationErrors),
            variant: "error",
          });
          return;
        }
        toast({
          title: t("linkGoogleDriveDocumentDialog.messages.successTitle"),
          description: t("linkGoogleDriveDocumentDialog.messages.linked"),
          variant: "success",
        });
        onSuccess(response.linkGoogleDriveDocument?.document?.id);
      },
      onError(err) {
        toast({
          title: t("linkGoogleDriveDocumentDialog.errors.title"),
          description: err.message,
          variant: "error",
        });
      },
    });
  };

  return (
    <form onSubmit={e => void handleSubmit(onSubmit)(e)}>
      <DialogContent className="grid grid-cols-[1fr_420px] max-h-[75vh]">
        {/* Left Column: File Selection */}
        <div className="p-6 flex flex-col gap-4 overflow-hidden">
          {googleDriveConnectors.length > 1 && (
            <div className="space-y-1">
              <Label>{t("linkGoogleDriveDocumentDialog.fields.connector")}</Label>
              <Select
                value={selectedConnectorId}
                onValueChange={v => {
                  setSelectedConnectorId(v);
                  setSelectedFile(null);
                }}
              >
                {googleDriveConnectors.map(c => (
                  <Option key={c.id} value={c.id}>
                    {c.displayName}
                  </Option>
                ))}
              </Select>
            </div>
          )}

          <div className="space-y-1">
            <Label>{t("linkGoogleDriveDocumentDialog.fields.searchFiles")}</Label>
            <Input
              icon={MagnifyingGlassIcon}
              placeholder={t("linkGoogleDriveDocumentDialog.fields.searchPlaceholder")}
              value={searchQuery}
              onValueChange={val => {
                setSearchQuery(val);
                handleSearchChange(val);
              }}
            />
          </div>

          <div className="flex-1 overflow-y-auto min-h-[260px] max-h-[320px] border border-border-primary rounded-md p-2">
            <Suspense
              fallback={(
                <div className="flex items-center justify-center p-8">
                  <Spinner />
                </div>
              )}
            >
              <DriveFileList
                connectorId={selectedConnectorId}
                query={debouncedQuery}
                selectedFileId={selectedFile?.id}
                onSelect={handleSelectFile}
              />
            </Suspense>
          </div>

          <div className="space-y-1">
            <Label htmlFor="title">{t("linkGoogleDriveDocumentDialog.fields.documentTitle")}</Label>
            <Input
              id="title"
              placeholder={t("linkGoogleDriveDocumentDialog.fields.titlePlaceholder")}
              {...register("title")}
            />
            {errors.title?.message && (
              <p className="text-xs text-red-500">{errors.title.message}</p>
            )}
          </div>
        </div>

        {/* Right Column: Document Properties */}
        <div className="py-5 px-6 bg-subtle border-l border-border-primary">
          <Label>{t("linkGoogleDriveDocumentDialog.properties.title")}</Label>
          <PropertyRow label={t("linkGoogleDriveDocumentDialog.properties.status")}>
            <Badge variant="neutral" size="md">
              {t("linkGoogleDriveDocumentDialog.status.draft")}
            </Badge>
          </PropertyRow>

          <PropertyRow label={t("linkGoogleDriveDocumentDialog.properties.source")}>
            <div className="flex items-center gap-1.5">
              <GoogleLogo className="size-4 shrink-0" />
              <Badge variant="neutral" size="md">
                Google Drive
              </Badge>
            </div>
          </PropertyRow>

          <PropertyRow
            id="documentType"
            label={t("linkGoogleDriveDocumentDialog.properties.type")}
            error={errors.documentType?.message}
          >
            <ControlledField
              control={control}
              name="documentType"
              type="select"
            >
              <DocumentTypeOptions />
            </ControlledField>
          </PropertyRow>

          <PropertyRow
            id="classification"
            label={t("linkGoogleDriveDocumentDialog.properties.classification")}
            error={errors.classification?.message}
          >
            <ControlledField
              control={control}
              name="classification"
              type="select"
            >
              <DocumentClassificationOptions />
            </ControlledField>
          </PropertyRow>

          <PropertyRow label={t("linkGoogleDriveDocumentDialog.properties.approvers")}>
            <PeopleMultiSelectField
              name="defaultApproverIds"
              control={control}
              organizationId={organizationId}
              placeholder={t("linkGoogleDriveDocumentDialog.fields.approversPlaceholder")}
            />
          </PropertyRow>
        </div>
      </DialogContent>

      <DialogFooter>
        <Button variant="secondary" type="button" onClick={onClose}>
          {t("linkGoogleDriveDocumentDialog.actions.cancel")}
        </Button>
        <Button type="submit" disabled={!selectedFile || isLinking}>
          {t("linkGoogleDriveDocumentDialog.actions.link")}
        </Button>
      </DialogFooter>
    </form>
  );
}

function DriveFileList({
  connectorId,
  query,
  selectedFileId,
  onSelect,
}: {
  connectorId: string;
  query: string;
  selectedFileId?: string;
  onSelect: (file: GoogleDriveFileItem) => void;
}) {
  const { t, i18n } = useTranslation();
  const data = useLazyLoadQuery<LinkGoogleDriveDocumentDialogFilesQuery>(
    filesQuery,
    { connectorId, query: query || null },
    { fetchPolicy: "network-only" },
  );

  const files = data.googleDriveFiles.files;

  if (files.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center text-txt-secondary text-sm">
        {t("linkGoogleDriveDocumentDialog.emptyFiles")}
      </div>
    );
  }

  return (
    <div className="space-y-1">
      {files.map(file => {
        const isSelected = file.id === selectedFileId;
        const isPdf = file.mimeType.toLowerCase().includes("pdf");
        const isDoc = file.mimeType.toLowerCase().includes("document");

        return (
          <div
            key={file.id}
            role="button"
            tabIndex={0}
            onClick={() => onSelect(file)}
            onKeyDown={e => {
              if (e.key === "Enter" || e.key === " ") {
                onSelect(file);
              }
            }}
            className={`flex items-center gap-3 p-2.5 rounded-md cursor-pointer border transition-colors ${
              isSelected
                ? "border-primary bg-bg-secondary"
                : "border-transparent hover:bg-subtle"
            }`}
          >
            <div className="shrink-0">
              {isPdf ? (
                <FilePdfIcon className="size-5 text-red-500" />
              ) : isDoc ? (
                <FileTextIcon className="size-5 text-blue-500" />
              ) : (
                <FileIcon className="size-5 text-txt-secondary" />
              )}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-txt-primary truncate">{file.name}</p>
              {file.modifiedTime && (
                <p className="text-xs text-txt-secondary">
                  {dateFormat(i18n.language, file.modifiedTime)}
                </p>
              )}
            </div>
            <div className="flex items-center gap-2 shrink-0">
              {file.webViewLink && (
                <a
                  href={file.webViewLink}
                  target="_blank"
                  rel="noreferrer"
                  onClick={e => e.stopPropagation()}
                  className="p-1 hover:text-txt-primary text-txt-tertiary"
                  title={t("linkGoogleDriveDocumentDialog.actions.previewInDrive")}
                >
                  <ArrowSquareOutIcon className="size-4" />
                </a>
              )}
              {isSelected && <IconCheckmark1 className="size-4 text-primary" />}
            </div>
          </div>
        );
      })}
    </div>
  );
}
