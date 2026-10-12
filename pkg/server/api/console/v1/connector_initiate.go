// Copyright (c) 2025-2026 Probo Inc <hello@probo.com>.
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

package console_v1

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"go.gearno.de/kit/httpserver"
	"go.gearno.de/kit/log"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"go.probo.inc/probo/pkg/connector"
	"go.probo.inc/probo/pkg/coredata"
	"go.probo.inc/probo/pkg/gid"
	"go.probo.inc/probo/pkg/iam"
	"go.probo.inc/probo/pkg/probo"
	"go.probo.inc/probo/pkg/saferedirect"
	"go.probo.inc/probo/pkg/server/api/authn"
)

var errInvalidReconnectConnector = errors.New("invalid reconnect connector")

func handleConnectorInitiate(
	logger *log.Logger,
	proboSvc *probo.Service,
	iamSvc *iam.Service,
	connectorRegistry *connector.Registry,
	safeRedirect *saferedirect.SafeRedirect,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")

		continueURL := r.URL.Query().Get("continue")
		if continueURL == "" {
			continueURL = r.URL.Query().Get("continue_url")
		}

		fail := func(status int, err error) {
			if continueURL != "" && safeRedirect != nil {
				parsedURL, parseErr := url.Parse(continueURL)
				if parseErr == nil {
					q := parsedURL.Query()
					q.Set("error", err.Error())
					parsedURL.RawQuery = q.Encode()
					safeRedirect.Redirect(w, r, parsedURL.String(), "/", http.StatusSeeOther)
					return
				}
			}

			httpserver.RenderError(w, status, err)
		}

		if provider == "" {
			fail(http.StatusBadRequest, fmt.Errorf("missing provider parameter"))
			return
		}

		organizationID, err := gid.ParseGID(r.URL.Query().Get("organization_id"))
		if err != nil {
			fail(http.StatusBadRequest, fmt.Errorf("invalid organization_id parameter"))
			return
		}

		if authn.APIKeyFromContext(r.Context()) != nil {
			fail(http.StatusBadRequest, fmt.Errorf("api key authentication cannot be used for this endpoint"))
			return
		}

		identity := authn.IdentityFromContext(r.Context())
		if identity == nil {
			fail(http.StatusUnauthorized, fmt.Errorf("authentication required"))
			return
		}

		session := authn.SessionFromContext(r.Context())
		if session == nil {
			fail(http.StatusUnauthorized, fmt.Errorf("authentication required"))
			return
		}

		scope, err := iamSvc.Authorizer.Authorize(r.Context(), iam.AuthorizeParams{
			Principal: identity.ID,
			Resource:  organizationID,
			Session:   &session.ID,
			Action:    probo.ActionConnectorInitiate,
		})
		if err != nil {
			fail(http.StatusForbidden, err)
			return
		}

		var connectorProvider coredata.ConnectorProvider
		if err := connectorProvider.UnmarshalText([]byte(provider)); err != nil {
			fail(http.StatusBadRequest, fmt.Errorf("unsupported provider: %q", provider))
			return
		}

		if saConnRaw, err := connectorRegistry.Lookup(provider, connector.ProtocolServiceAccount); err == nil {
			if saConnector, ok := saConnRaw.(*connector.ServiceAccountConnector); ok {
				if connectorProvider == coredata.ConnectorProviderGoogleDrive {
					driveSvc, err := drive.NewService(
						r.Context(),
						option.WithCredentialsJSON([]byte(saConnector.CredentialsJSON)),
						option.WithScopes(drive.DriveReadonlyScope),
					)
					if err != nil {
						fail(http.StatusBadRequest, fmt.Errorf("invalid Google Drive service account credentials: %w", err))
						return
					}

					_, err = driveSvc.Files.List().
						PageSize(1).
						SupportsAllDrives(true).
						IncludeItemsFromAllDrives(true).
						Corpora("allDrives").
						Context(r.Context()).
						Do()
					if err != nil {
						fail(http.StatusBadRequest, fmt.Errorf("cannot access Google Drive with service account: %w", err))
						return
					}
				}

				existing, err := loadExistingConnector(r, proboSvc, scope)
				if err != nil && !errors.Is(err, coredata.ErrResourceNotFound) {
					fail(http.StatusBadRequest, err)
					return
				}

				if existing == nil {
					allConns, err := proboSvc.Connectors.ListAllForOrganizationID(r.Context(), scope, organizationID)
					if err == nil {
						for _, c := range allConns {
							if c.Provider == connectorProvider {
								existing, _ = proboSvc.Connectors.GetWithConnection(r.Context(), scope, c.ID)
								break
							}
						}
					}
				}

				connObj := &connector.ServiceAccountConnection{
					FolderID: saConnector.FolderID,
				}

				if existing != nil {
					_, err = proboSvc.Connectors.Reconnect(r.Context(), scope, probo.ReconnectConnectorRequest{
						ConnectorID:    existing.ID,
						OrganizationID: organizationID,
						Provider:       connectorProvider,
						Connection:     connObj,
					})
					if err != nil {
						logger.ErrorCtx(r.Context(), "cannot reconnect service account connector", log.Error(err))
						fail(http.StatusInternalServerError, fmt.Errorf("cannot reconnect connector: %w", err))
						return
					}
				} else {
					_, err = proboSvc.Connectors.Create(r.Context(), scope, probo.CreateConnectorRequest{
						OrganizationID: organizationID,
						Provider:       connectorProvider,
						Protocol:       coredata.ConnectorProtocolServiceAccount,
						Connection:     connObj,
					})
					if err != nil {
						logger.ErrorCtx(r.Context(), "cannot create service account connector", log.Error(err))
						fail(http.StatusInternalServerError, fmt.Errorf("cannot create connector: %w", err))
						return
					}
				}

				if continueURL != "" && safeRedirect != nil {
					safeRedirect.Redirect(w, r, continueURL, "/", http.StatusSeeOther)
					return
				}

				http.Redirect(w, r, fmt.Sprintf("/organizations/%s", organizationID), http.StatusSeeOther)
				return
			}
		}

		if _, err := connectorRegistry.Lookup(provider, connector.ProtocolOAuth2); err != nil {
			fail(http.StatusBadRequest, fmt.Errorf("provider %q is not configured on this server (missing OAuth2 or Service Account credentials)", provider))
			return
		}

		requestedScopes := r.URL.Query()["scope"]
		prb := proboSvc

		// Look up the connector this flow reconnects, if any, so we can
		// union its stored scopes into the new auth request.
		// Cross-org/provider/protocol mismatches are caught inside
		// Reconnect at callback time; this handler only needs the scope
		// set.
		existing, err := loadExistingConnector(r, prb, scope)
		if err != nil {
			if errors.Is(err, coredata.ErrResourceNotFound) {
				fail(http.StatusBadRequest, fmt.Errorf("cannot reconnect: connector not found"))
				return
			}

			if errors.Is(err, errInvalidReconnectConnector) {
				fail(http.StatusBadRequest, err)
				return
			}

			logger.ErrorCtx(r.Context(), "cannot look up existing connector", log.Error(err))
			fail(http.StatusInternalServerError, fmt.Errorf("cannot look up existing connector: %w", err))

			return
		}

		// Hand the earlier grant to the connector rather than unioning it
		// here: whether a reconnect may widen the request or must ask for
		// exactly the registered scopes is a per-provider OAuth trait. No
		// short-circuit either way — every reconnect runs the full OAuth
		// flow so revoked or stale tokens are never silently reused.
		opts := connector.InitiateOptions{Scopes: requestedScopes, Site: r.URL.Query().Get("site")}
		if existing != nil {
			opts.GrantedScopes = existing.Connection.Scopes()
			opts.IncludeGrantedScopes = true
			opts.ConnectorID = existing.ID.String()
		}

		redirectURL, err := connectorRegistry.Initiate(
			r.Context(),
			provider,
			organizationID,
			opts,
			r,
		)
		if err != nil {
			logger.ErrorCtx(r.Context(), "cannot initiate connector", log.Error(err))
			fail(http.StatusInternalServerError, fmt.Errorf("cannot initiate connector: %w", err))

			return
		}

		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	}
}

// loadExistingConnector returns the connector the initiate handler
// should reconnect, or nil for a fresh connect. Reconnect is explicit:
// only a connector_id query parameter selects a row — a bare initiate
// always creates a new connector, so an organization can connect the
// same provider several times. Callers must distinguish
// ErrResourceNotFound (explicit id not found — 400) from nil
// (fresh-connect path).
func loadExistingConnector(
	r *http.Request,
	prb *probo.Service,
	scope coredata.Scoper,
) (*coredata.Connector, error) {
	explicitID := r.URL.Query().Get("connector_id")
	if explicitID == "" {
		return nil, nil
	}

	parsedID, err := gid.ParseGID(explicitID)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot parse connector id: %w", errInvalidReconnectConnector, err)
	}

	return prb.Connectors.GetWithConnection(r.Context(), scope, parsedID)
}
