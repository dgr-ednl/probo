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

import { useCallback, useEffect, useState } from "react";
import { graphql } from "react-relay";

import type { useExportDocumentPdfMutation } from "#/__generated__/core/useExportDocumentPdfMutation.graphql";
import { useMutation } from "#/lib/relay/useMutation";

const exportDocumentPdfMutation = graphql`
  mutation useExportDocumentPdfMutation(
    $input: ExportDocumentVersionPDFInput!
  ) {
    exportDocumentVersionPDF(input: $input) {
      data
    }
  }
`;

type ExportedPdf = {
  documentVersionId: string;
  dataUri: string;
};

export function useExportDocumentPdf(
  documentVersionId: string | null,
  reloadKey?: string | number | null,
): {
  dataUri: string | null;
  isLoading: boolean;
  error: Error | null;
  reload: () => void;
} {
  const [exportPdf] = useMutation<useExportDocumentPdfMutation>(
    exportDocumentPdfMutation,
  );
  const [exported, setExported] = useState<ExportedPdf | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [reloadTrigger, setReloadTrigger] = useState(0);

  const reload = useCallback(() => {
    setReloadTrigger(t => t + 1);
  }, []);

  useEffect(() => {
    if (!documentVersionId) {
      setExported(null);
      setIsLoading(false);
      setError(null);
      return;
    }

    let cancelled = false;
    setIsLoading(true);
    setError(null);

    void exportPdf(
      {
        variables: {
          input: {
            documentVersionId,
            withSignatures: false,
            withWatermark: false,
          },
        },
      },
      { errorToast: false },
    )
      .then((response) => {
        if (!cancelled) {
          setExported({
            documentVersionId,
            dataUri: response.exportDocumentVersionPDF.data,
          });
          setIsLoading(false);
        }
      })
      .catch((err: Error) => {
        if (!cancelled) {
          setError(err);
          setIsLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [documentVersionId, reloadKey, reloadTrigger, exportPdf]);

  const hasMatchingData = exported?.documentVersionId === documentVersionId;

  return {
    dataUri: hasMatchingData ? exported.dataUri : null,
    isLoading: isLoading || (Boolean(documentVersionId) && !hasMatchingData && !error),
    error,
    reload,
  };
}
