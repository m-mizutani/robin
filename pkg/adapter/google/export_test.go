package google

import "net/http"

// NewOAuthForTest points every endpoint at baseURL (/auth, /token, /userinfo,
// /revoke) so tests can serve them with httptest.
func NewOAuthForTest(clientID, clientSecret, baseURL string) *OAuth {
	o := NewOAuth(clientID, clientSecret)
	o.authURL = baseURL + "/auth"
	o.tokenURL = baseURL + "/token"
	o.userinfoURL = baseURL + "/userinfo"
	o.revokeURL = baseURL + "/revoke"
	o.httpClient = http.DefaultClient
	return o
}

// NewWorkspaceClientFactoryForTest serves the token endpoint at
// baseURL+"/token" and the Gmail, Drive and Calendar APIs below baseURL with
// their usual paths (/gmail/v1/..., /drive/v3/..., /calendar/v3/...).
func NewWorkspaceClientFactoryForTest(clientID, clientSecret, baseURL string) *WorkspaceClientFactory {
	f := NewWorkspaceClientFactory(clientID, clientSecret, http.DefaultClient)
	f.tokenURL = baseURL + "/token"
	f.gmailEndpoint = baseURL + "/"
	f.driveEndpoint = baseURL + "/drive/v3/"
	f.calendarEndpoint = baseURL + "/calendar/v3/"
	return f
}

var DriveQueryStringForTest = driveQueryString
