package github

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"

	"github.com/pkg/errors"

	"github.com/bradleyfalzon/gopherci/internal/analyser"
	"github.com/bradleyfalzon/gopherci/internal/db"
	"github.com/dgrijalva/jwt-go"
)

const (
	// acceptHeader is the GitHub Integrations Preview Accept header.
	acceptHeader = "application/vnd.github.machine-man-preview+json"
)

// GitHub is the type gopherci uses to interract with github.com.
type GitHub struct {
	db            db.DB
	analyser      analyser.Analyser
	integrationID int               // id is the integration id
	keyFile       string            // keyFile is the path to private key
	tr            http.RoundTripper // tr is a transport shared by all installations to reuse http connections
	baseURL       string            // baseURL for GitHub API
}

// New returns a GitHub object for use with GitHub integrations
// https://developer.github.com/changes/2016-09-14-Integrations-Early-Access/
// integrationID is the GitHub Integration ID (not installation ID), keyFile is the path to the
// private key provided to you by GitHub during the integration registration.
func New(analyser analyser.Analyser, db db.DB, integrationID, keyFile string) (*GitHub, error) {
	iid, err := strconv.ParseInt(integrationID, 10, 64)
	if err != nil {
		return nil, errors.Wrap(err, "could not parse integrationID")
	}

	g := &GitHub{
		analyser:      analyser,
		db:            db,
		integrationID: int(iid),
		keyFile:       keyFile,
		tr:            http.DefaultTransport,
		baseURL:       "https://api.github.com",
	}

	// TODO some prechecks should be done now, instead of later, fail fast/early.

	return g, nil
}

// installationTransport provides a http.RoundTripper by wrapping an existing
// http.RoundTripper (that's shared between multiple installation transports to
// reuse underlying http connections), but provides GitHub Integration
// authentication as an installation.
//
// See https://developer.github.com/early-access/integrations/authentication/#as-an-installation
type installationTransport struct {
	baseURL        string            // baseURL for GitHub API
	tr             http.RoundTripper // tr is the underlying roundtripper being wrapped
	keyFile        string            // keyFile is the path to GitHub Intregration's PEM encoded private key
	integrationID  int               // integrationID is the GitHub Integration's Installation ID
	installationID int               // installationID is the GitHub Integration's Installation ID
	token          *accessToken      // token is the installation's access token
}

// accessToken is an installation access token
type accessToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (g *GitHub) newInstallationTransport(installationID int) *installationTransport {
	return &installationTransport{
		baseURL:        g.baseURL,
		tr:             g.tr,
		keyFile:        g.keyFile,
		integrationID:  g.integrationID,
		installationID: installationID,
	}
}

func (t *installationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token == nil || t.token.ExpiresAt.Add(-time.Minute).Before(time.Now()) {
		// Token is not set or expired/nearly expired, so refresh
		if err := t.refreshToken(); err != nil {
			return nil, errors.Wrap(err, "could not refresh installation token")
		}
	}

	req.Header.Set("Authorization", "token "+t.token.Token)
	req.Header.Set("Accept", acceptHeader)
	resp, err := t.tr.RoundTrip(req)
	return resp, err
}

func (t *installationTransport) refreshToken() error {
	// TODO these claims could probably be reused between installations before expiry
	claims := &jwt.StandardClaims{
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		Issuer:    strconv.Itoa(t.integrationID),
	}
	bearer := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	key, err := ioutil.ReadFile(t.keyFile)
	if err != nil {
		return errors.Wrap(err, "could not read private key")
	}

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(key)
	if err != nil {
		return errors.Wrap(err, "could not parse private key")
	}

	ss, err := bearer.SignedString(privateKey)
	if err != nil {
		return errors.Wrap(err, "could not sign jwt")
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%v/installations/%v/access_tokens", t.baseURL, t.installationID), nil)
	if err != nil {
		return errors.Wrap(err, "could not create request")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", ss))
	req.Header.Set("Accept", acceptHeader)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "could not get access_tokens")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("received response status %q when fetching %v", resp.Status, req.URL)
	}

	if err := json.NewDecoder(resp.Body).Decode(&t.token); err != nil {
		return errors.Wrap(err, "could not decode json response from access_tokens")
	}

	return nil
}
