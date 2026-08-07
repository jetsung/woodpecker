// Copyright 2024 Woodpecker Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package atomgit

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"

	"go.woodpecker-ci.org/woodpecker/v3/server"
	"go.woodpecker-ci.org/woodpecker/v3/server/forge"
	"go.woodpecker-ci.org/woodpecker/v3/server/forge/common"
	forge_types "go.woodpecker-ci.org/woodpecker/v3/server/forge/types"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
	"go.woodpecker-ci.org/woodpecker/v3/server/store"
	"go.woodpecker-ci.org/woodpecker/v3/shared/httputil"
)

const (
	defaultPageSize = 50

	authorizeTokenURL = "%s/oauth/authorize"
	accessTokenURL    = "%s/oauth/token"
	apiPath           = "/api/v5"
)

// AtomGit implements the forge.Forge interface for https://atomgit.com.
type AtomGit struct {
	id                int64
	url               string
	oAuthClientID     string
	oAuthClientSecret string
	oAuthHost         string
	skipVerify        bool
	pageSize          int
}

// Opts defines configuration options for the AtomGit driver.
type Opts struct {
	URL               string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthHost         string
	SkipVerify        bool
}

// New returns a Forge implementation that integrates with AtomGit.
func New(id int64, opts Opts) (forge.Forge, error) {
	return &AtomGit{
		id:                id,
		url:               opts.URL,
		oAuthClientID:     opts.OAuthClientID,
		oAuthClientSecret: opts.OAuthClientSecret,
		oAuthHost:         opts.OAuthHost,
		skipVerify:        opts.SkipVerify,
	}, nil
}

// Name returns the unique identifier of this driver.
func (c *AtomGit) Name() string { return "atomgit" }

// URL returns the root url of the configured forge.
func (c *AtomGit) URL() string { return c.url }

func (c *AtomGit) httpClient() *http.Client {
	if c.skipVerify {
		return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	}
	return &http.Client{}
}

func (c *AtomGit) oauth2Config(ctx context.Context) (*oauth2.Config, context.Context) {
	publicOAuthURL := c.oAuthHost
	if publicOAuthURL == "" {
		publicOAuthURL = c.url
	}
	return &oauth2.Config{
			ClientID:     c.oAuthClientID,
			ClientSecret: c.oAuthClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL:  fmt.Sprintf(authorizeTokenURL, publicOAuthURL),
				TokenURL: fmt.Sprintf(accessTokenURL, publicOAuthURL),
			},
			RedirectURL: fmt.Sprintf("%s/authorize", server.Config.Server.OAuthHost),
		},
		context.WithValue(ctx, oauth2.HTTPClient, httputil.WrapClient(c.httpClient(), "forge-atomgit"))
}

// Login authenticates a user via AtomGit OAuth2.
func (c *AtomGit) Login(ctx context.Context, req *forge_types.OAuthRequest) (*model.User, string, error) {
	config, oauth2Ctx := c.oauth2Config(ctx)
	redirectURL := config.AuthCodeURL(req.State)

	if len(req.Code) == 0 {
		return nil, redirectURL, nil
	}

	token, err := config.Exchange(oauth2Ctx, req.Code)
	if err != nil {
		return nil, redirectURL, fmt.Errorf("oauth2 config exchange failed: %w", err)
	}

	account, err := c.getCurrentUser(oauth2Ctx, token.AccessToken)
	if err != nil {
		return nil, redirectURL, fmt.Errorf("fetching user info failed: %w", err)
	}

	return &model.User{
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		Expiry:        token.Expiry.UTC().Unix(),
		Login:         account.Username,
		Email:         account.Email,
		ForgeRemoteID: model.ForgeRemoteID(account.ID),
		Avatar:        expandAvatar(account.HTMLURL, account.AvatarURL),
	}, redirectURL, nil
}

// Refresh refreshes the AtomGit oauth2 access token.
func (c *AtomGit) Refresh(ctx context.Context, user *model.User) (bool, error) {
	config, oauth2Ctx := c.oauth2Config(ctx)
	config.RedirectURL = ""

	source := config.TokenSource(oauth2Ctx, &oauth2.Token{
		AccessToken:  user.AccessToken,
		RefreshToken: user.RefreshToken,
		Expiry:       time.Unix(user.Expiry, 0),
	})

	token, err := source.Token()
	if err != nil || len(token.AccessToken) == 0 {
		return false, err
	}

	user.AccessToken = token.AccessToken
	user.RefreshToken = token.RefreshToken
	user.Expiry = token.Expiry.UTC().Unix()
	return true, nil
}

func (c *AtomGit) getCurrentUser(ctx context.Context, token string) (*user, error) {
	out := new(user)
	apiURL := fmt.Sprintf("%s%s/user", c.url, apiPath)
	if err := c.get(ctx, token, apiURL, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Teams returns the organizations the user belongs to.
func (c *AtomGit) Teams(ctx context.Context, u *model.User, p *model.ListOptions) ([]*model.Team, error) {
	if p.Page != 1 {
		return nil, nil
	}

	// /users/orgs returns a bare JSON array; the {"data": [...]} wrapper only
	// appears in local fixtures.
	out := make([]*namespace, 0)
	apiURL := fmt.Sprintf("%s%s/users/orgs", c.url, apiPath)
	if err := c.get(ctx, u.AccessToken, apiURL, &out); err != nil {
		return nil, err
	}

	teams := make([]*model.Team, 0, len(out))
	for _, org := range out {
		teams = append(teams, toTeam(org, c.url))
	}
	return teams, nil
}

// Repo fetches a single repository by remote ID or owner/name.
func (c *AtomGit) Repo(ctx context.Context, u *model.User, remoteID model.ForgeRemoteID, owner, name string) (*model.Repo, error) {
	// Prefer the full repository lookup by owner/name: GET /repos/:owner/:name
	// is the only endpoint that reliably carries html_url. Resolving by id alone
	// can return an incomplete payload (no html_url), leaving forge_url empty.
	if owner != "" && name != "" {
		if full, err := c.getRepoByName(ctx, u.AccessToken, owner, name); err == nil {
			return toRepo(full), nil
		}
		// On error, fall through to the remoteID-based resolution below.
	}

	// There is no single-repo lookup by numeric id (only /repos/{owner}/{repo}),
	// so resolve a remoteID by scanning the authenticated user's repositories.
	if remoteID.IsValid() {
		if scanned, serr := c.getRepoByIDScan(ctx, u, remoteID); serr == nil {
			return scanned, nil
		}
	}

	if owner != "" && name != "" {
		out, err := c.getRepoByName(ctx, u.AccessToken, owner, name)
		if err != nil {
			return nil, err
		}
		return toRepo(out), nil
	}

	return nil, forge_types.ErrRepoNotFound
}

// getRepoByIDScan lists the authenticated user's repositories and returns the
// one whose id matches remoteID. AtomGit does not expose a single-repo lookup
// by id, so this is how Repo() resolves a remoteID without owner/name.
func (c *AtomGit) getRepoByIDScan(ctx context.Context, u *model.User, remoteID model.ForgeRemoteID) (*model.Repo, error) {
	page := 1
	for {
		apiURL := fmt.Sprintf("%s%s/user/repos?page=%d&per_page=100", c.url, apiPath, page)
		repos := make([]*repository, 0)
		if err := c.get(ctx, u.AccessToken, apiURL, &repos); err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}
		for _, r := range repos {
			if string(r.ID) == string(remoteID) {
			// The /user/repos summary omits fields the UI needs (html_url, ...),
			// so upgrade to the full repository object via GET /repos/:owner/:name.
			// The summary has no namespace object, so derive owner/name from
			// path_with_namespace / full_name.
				fullName := r.FullName
				if fullName == "" {
					fullName = r.PathWithNamespace
				}
				owner, name := repoOwnerName(fullName)
				if owner != "" && name != "" {
					if full, ferr := c.getRepoByName(ctx, u.AccessToken, owner, name); ferr == nil {
						return toRepo(full), nil
					}
				}
				return toRepo(r), nil
			}
		}
		if len(repos) < 100 {
			break
		}
		page++
	}
	return nil, forge_types.ErrRepoNotFound
}

func (c *AtomGit) getRepoByName(ctx context.Context, token, owner, name string) (*repository, error) {
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s", c.url, apiPath, owner, name)
	out := new(repository)
	if err := c.get(ctx, token, apiURL, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Repos fetches all repositories accessible to the user.
func (c *AtomGit) Repos(ctx context.Context, u *model.User, p *model.ListOptions) ([]*model.Repo, error) {
	if p.Page != 1 {
		return nil, nil
	}

	// /user/repos returns a bare JSON array of repositories; the
	// {"data": [...]} wrapper only appears in local fixtures.
	out := make([]*repository, 0)
	apiURL := fmt.Sprintf("%s%s/user/repos", c.url, apiPath)
	if err := c.get(ctx, u.AccessToken, apiURL, &out); err != nil {
		return nil, err
	}

	result := make([]*model.Repo, 0, len(out))
	for _, repo := range out {
		if repo.Archived.Bool() {
			continue
		}
		result = append(result, toRepo(repo))
	}
	return result, nil
}

// File fetches a single file at a specific commit.
func (c *AtomGit) File(ctx context.Context, u *model.User, r *model.Repo, b *model.Pipeline, fileName string) ([]byte, error) {
	cleanPath := strings.TrimPrefix(fileName, "/")
	parts := strings.Split(cleanPath, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	encoded := strings.Join(parts, "/")

	// NOTE: /raw/{path} identifies the revision with "ref", while /file_list
	// (see Dir) uses "ref_name". This is the API contract, not an inconsistency
	// to clean up: an unknown query parameter is silently ignored and the
	// default branch is returned, so swapping either name produces wrong
	// results with no error.
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/raw/%s?ref=%s", c.url, apiPath, r.Owner, r.Name, encoded, b.Commit)
	body, status, err := c.getRaw(ctx, u.AccessToken, apiURL)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, errors.Join(err, &forge_types.ErrConfigNotFound{Configs: []string{fileName}})
	}
	if status >= http.StatusBadRequest {
		return nil, fmt.Errorf("could not fetch file: status %d", status)
	}
	return body, nil
}

// Dir fetches all files in a directory at a specific commit.
func (c *AtomGit) Dir(ctx context.Context, u *model.User, r *model.Repo, b *model.Pipeline, dirName string) ([]*forge_types.FileMeta, error) {
	if r.Owner == "" || r.Name == "" {
		return nil, &forge_types.ErrConfigNotFound{Configs: []string{dirName}}
	}

	// NOTE: /file_list identifies the revision with "ref_name", NOT "ref" (which
	// is what /raw/{path} takes, see File). An unknown query parameter is silently
	// ignored and the default branch tree is returned, so sending "ref" here made
	// a push to any other branch only discover the pipeline configs that also
	// existed on the default branch - with no error logged.
	//
	// /file_list also cannot be scoped to a directory (its only parameters are
	// ref_name and file_name, a filename search), so the whole repository tree
	// must be listed and filtered by prefix below.
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/file_list?ref_name=%s", c.url, apiPath, r.Owner, r.Name, b.Commit)
	// /file_list returns either a bare JSON array of file path strings
	// (e.g. [".woodpecker/test.yaml","README.md",...]) or an array of objects
	// ([{"path":...,"type":...,"name":...}]) depending on the API version /
	// fixture. Both shapes are parsed below, and the {"data": [...]} envelope is
	// unwrapped transparently by get().
	rawBody, status, err := c.getRaw(ctx, u.AccessToken, apiURL)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, errors.Join(err, &forge_types.ErrConfigNotFound{Configs: []string{dirName}})
	}
	if status >= http.StatusBadRequest {
		return nil, fmt.Errorf("could not fetch file list: status %d", status)
	}
	names, perr := parseFileList(rawBody)
	if perr != nil {
		return nil, perr
	}

	cleanDir := strings.Trim(dirName, " /")
	var matched []string
	for _, name := range names {
		cleanName := strings.TrimPrefix(name, "/")

		// Filter files by directory if dirName was specified (e.g. ".woodpecker")
		if cleanDir != "" && cleanDir != "." {
			if cleanName != cleanDir && !strings.HasPrefix(cleanName, cleanDir+"/") {
				continue
			}
		}

		// Because /file_list returns the entire repository tree, filter by
		// extension before fetching: every surviving entry costs one /raw
		// request. Honour the operator-configured extension set rather than a
		// hardcoded list, which would override WOODPECKER_DEFAULT_PIPELINE_CONFIG_EXTENSIONS.
		ext := strings.ToLower(filepath.Ext(cleanName))
		if slices.Contains(configExtensions(), ext) {
			matched = append(matched, name)
		}
	}

	if len(matched) == 0 {
		return nil, &forge_types.ErrConfigNotFound{Configs: []string{dirName}}
	}

	var configs []*forge_types.FileMeta
	for _, name := range matched {
		data, err := c.File(ctx, u, r, b, name)
		if err != nil {
			if errors.Is(err, &forge_types.ErrConfigNotFound{}) {
				continue
			}
			return nil, fmt.Errorf("multi-pipeline cannot get %s: %w", name, err)
		}
		configs = append(configs, &forge_types.FileMeta{Name: name, Data: data})
	}

	if len(configs) == 0 {
		return nil, &forge_types.ErrConfigNotFound{Configs: []string{dirName}}
	}

	return configs, nil
}

// defaultConfigExtensions is the fallback used when the server config has not
// been populated (unit tests, embedded use). Filtering on an empty set would
// discard every file and leave the repository with no pipelines at all.
var defaultConfigExtensions = []string{".yaml", ".yml"}

// configExtensions returns the pipeline config file extensions to accept when
// listing a config directory, lowercased for comparison against filepath.Ext.
func configExtensions() []string {
	configured := server.Config.Pipeline.ConfigExtensions
	if len(configured) == 0 {
		return defaultConfigExtensions
	}
	out := make([]string, 0, len(configured))
	for _, ext := range configured {
		out = append(out, strings.ToLower(ext))
	}
	return out
}

// fileObj is the object form returned by some AtomGit /file_list responses,
// where each entry carries a "path" field instead of being a bare string.
type fileObj struct {
	Path string `json:"path"`
}

// parseFileList decodes a /file_list response that may arrive as a bare JSON
// array of path strings, an array of objects with a "path" field, or a
// {"data": [...]} envelope wrapping either shape.
func parseFileList(body []byte) ([]string, error) {
	// Fast path: bare array of strings.
	var strArr []string
	if err := json.Unmarshal(body, &strArr); err == nil {
		return strArr, nil
	}

	// Object array form.
	var objArr []fileObj
	if err := json.Unmarshal(body, &objArr); err == nil {
		names := make([]string, 0, len(objArr))
		for _, o := range objArr {
			if o.Path != "" {
				names = append(names, o.Path)
			}
		}
		return names, nil
	}

	// Envelope form: {"data": [...]}.
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err == nil && len(env.Data) > 0 {
		if names, e := parseFileList(env.Data); e == nil {
			return names, nil
		}
	}

	preview := body
	if len(preview) > 240 {
		preview = preview[:240]
	}
	return nil, fmt.Errorf("atomgit file_list: could not decode response (body: %s)", string(preview))
}

// Status posts pipeline status to AtomGit. AtomGit does not expose a commit
// status API, so this is a best-effort no-op that does not block pipelines.
func (c *AtomGit) Status(_ context.Context, _ *model.User, _ *model.Repo, _ *model.Pipeline, _ *model.Workflow) error {
	log.Debug().Msg("atomgit does not support commit status updates; skipping")
	return nil
}

// Netrc returns netrc credentials for cloning the repository.
func (c *AtomGit) Netrc(u *model.User, r *model.Repo) (*model.Netrc, error) {
	login := ""
	token := ""
	if u != nil {
		login = u.Login
		token = u.AccessToken
	}

	host, err := common.ExtractHostFromCloneURL(r.Clone)
	if err != nil {
		return nil, err
	}

	return &model.Netrc{
		Login:    login,
		Password: token,
		Machine:  host,
		Type:     model.ForgeTypeAtomGit,
	}, nil
}

// Activate registers a webhook pointing to Woodpecker.
func (c *AtomGit) Activate(ctx context.Context, u *model.User, r *model.Repo, link string) error {
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/hooks", c.url, apiPath, r.Owner, r.Name)
	body := map[string]any{
		"url":                   link,
		"password":              r.Hash,
		"push_events":           true,
		"tag_push_events":       true,
		"merge_requests_events": true,
		"issues_events":         false,
		"note_events":           false,
	}
	_, status, err := c.post(ctx, u.AccessToken, apiURL, body)
	if err != nil {
		return err
	}
	if status >= http.StatusBadRequest {
		return fmt.Errorf("could not create webhook: status %d", status)
	}
	return nil
}

// Deactivate removes the webhook if present.
func (c *AtomGit) Deactivate(ctx context.Context, u *model.User, r *model.Repo, link string) error {
	hooks, err := c.listHooks(ctx, u.AccessToken, r)
	if err != nil {
		return err
	}

	linkURL, err := url.Parse(link)
	if err != nil {
		return err
	}

	for _, h := range hooks {
		hookURL, err := url.Parse(h.URL)
		if err == nil && hookURL.Host == linkURL.Host && h.Password == r.Hash {
			delURL := fmt.Sprintf("%s%s/repos/%s/%s/hooks/%s", c.url, apiPath, r.Owner, r.Name, h.ID)
			if _, status, err := c.delete(ctx, u.AccessToken, delURL); err != nil {
				return err
			} else if status >= http.StatusBadRequest {
				return fmt.Errorf("could not delete webhook: status %d", status)
			}
			return nil
		}
	}
	return nil
}

func (c *AtomGit) listHooks(ctx context.Context, token string, r *model.Repo) ([]*hook, error) {
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/hooks", c.url, apiPath, r.Owner, r.Name)
	// AtomGit's /hooks returns a bare JSON array (the {"data": [...]} wrapper only
	// appears in local fixtures).
	out := make([]*hook, 0)
	if err := c.get(ctx, token, apiURL, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Branches returns all branch names for the repository.
func (c *AtomGit) Branches(ctx context.Context, u *model.User, r *model.Repo, p *model.ListOptions) ([]string, error) {
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/branches", c.url, apiPath, r.Owner, r.Name)
	var branches []*branch
	if err := c.get(ctx, common.UserToken(ctx, r, u), apiURL, &branches); err != nil {
		return nil, err
	}
	result := make([]string, len(branches))
	for i, b := range branches {
		result[i] = b.Name
	}
	return result, nil
}

// BranchHead returns the latest commit SHA for a branch.
func (c *AtomGit) BranchHead(ctx context.Context, u *model.User, r *model.Repo, branchName string) (*model.Commit, error) {
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/branches/%s", c.url, apiPath, r.Owner, r.Name, branchName)
	out := new(branch)
	if err := c.get(ctx, common.UserToken(ctx, r, u), apiURL, out); err != nil {
		return nil, err
	}
	if out.Commit == nil {
		return nil, fmt.Errorf("branch %s has no commit", branchName)
	}

	// The branch endpoint returns the commit SHA either as "id" or "sha";
	// prefer whichever is populated.
	sha := out.Commit.ID
	if sha == "" {
		sha = out.Commit.SHA
	}

	// The branch endpoint's embedded commit object does not carry a
	// browser-facing commit page URL, so out.Commit.URL is empty in practice.
	// Manual pipelines copy this into Pipeline.ForgeURL, and an empty forge_url
	// renders as <a href=""> which reloads the current page instead of opening
	// the commit. Synthesize the commit page URL from the repository forge URL
	// using the {repo}/commits/detail/{sha} scheme, and only trust the
	// API-provided URL when it is an absolute http(s) URL.
	forgeURL := out.Commit.URL
	if !strings.HasPrefix(forgeURL, "http://") && !strings.HasPrefix(forgeURL, "https://") {
		forgeURL = ""
	}
	if forgeURL == "" && sha != "" {
		forgeURL = fmt.Sprintf("%s/commits/detail/%s", r.ForgeURL, sha)
	}

	return &model.Commit{
		SHA:      sha,
		ForgeURL: forgeURL,
	}, nil
}

// PullRequests returns open pull requests for the repository.
func (c *AtomGit) PullRequests(ctx context.Context, u *model.User, r *model.Repo, p *model.ListOptions) ([]*model.PullRequest, error) {
	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/pulls?state=open", c.url, apiPath, r.Owner, r.Name)
	var prs []*pullRequest
	if err := c.get(ctx, common.UserToken(ctx, r, u), apiURL, &prs); err != nil {
		return nil, err
	}
	result := make([]*model.PullRequest, len(prs))
	for i, pr := range prs {
		result[i] = &model.PullRequest{
			Index: model.ForgeRemoteID(pr.IID),
			Title: pr.Title,
		}
	}
	return result, nil
}

// Hook parses an incoming AtomGit webhook.
func (c *AtomGit) Hook(ctx context.Context, r *http.Request) (*model.Repo, *model.Pipeline, error) {
	repo, pipeline, err := parseHook(r)
	if err != nil {
		return nil, nil, err
	}

	if pipeline != nil && pipeline.IsPullRequest() && len(pipeline.ChangedFiles) == 0 {
		index, err := strconv.ParseInt(strings.Split(pipeline.Ref, "/")[2], 10, 64)
		if err != nil {
			return nil, nil, err
		}
		pipeline.ChangedFiles, err = c.getChangedFilesForPR(ctx, repo, index)
		if err != nil {
			log.Error().Err(err).Msgf("could not get changed files for PR %s#%d", repo.FullName, index)
		}
	}

	return repo, pipeline, nil
}

func (c *AtomGit) getChangedFilesForPR(ctx context.Context, repo *model.Repo, index int64) ([]string, error) {
	_store, ok := store.TryFromContext(ctx)
	if !ok {
		log.Error().Msg("could not get store from context")
		return []string{}, nil
	}

	repo, err := _store.GetRepoNameFallback(c.id, repo.ForgeRemoteID, repo.FullName)
	if err != nil {
		return nil, err
	}
	user, err := _store.GetUser(repo.UserID)
	if err != nil {
		return nil, err
	}

	forge.Refresh(ctx, c, _store, user)

	apiURL := fmt.Sprintf("%s%s/repos/%s/%s/pulls/%d/files", c.url, apiPath, repo.Owner, repo.Name, index)
	var files []*commitFile
	if err := c.get(ctx, user.AccessToken, apiURL, &files); err != nil {
		return nil, err
	}

	changed := make([]string, 0, len(files))
	for _, f := range files {
		if f.NewPath != "" {
			changed = append(changed, f.NewPath)
		} else if f.OldPath != "" {
			changed = append(changed, f.OldPath)
		}
	}
	return changed, nil
}

// OrgMembership checks if the user is a member of the organization.
func (c *AtomGit) OrgMembership(ctx context.Context, u *model.User, org string) (*model.OrgPerm, error) {
	apiURL := fmt.Sprintf("%s%s/orgs/%s/members/%s", c.url, apiPath, org, u.Login)
	_, status, err := c.getRaw(ctx, u.AccessToken, apiURL)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return &model.OrgPerm{}, nil
	}
	if status >= http.StatusBadRequest {
		return &model.OrgPerm{}, nil
	}
	// AtomGit does not expose detailed permission levels via this endpoint;
	// membership is enough to return admin capability conservatively.
	return &model.OrgPerm{Member: true, Admin: false}, nil
}

// Org fetches an organization (or user) from AtomGit.
func (c *AtomGit) Org(ctx context.Context, u *model.User, org string) (*model.Org, error) {
	apiURL := fmt.Sprintf("%s%s/users/%s", c.url, apiPath, org)
	out := new(user)
	if err := c.get(ctx, u.AccessToken, apiURL, out); err != nil {
		return nil, err
	}
	return &model.Org{
		Name:   out.Username,
		IsUser: true,
	}, nil
}

// --- HTTP helpers ---

func (c *AtomGit) request(ctx context.Context, method, token, apiURL string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, apiURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	client := httputil.WrapClient(c.httpClient(), "forge-atomgit")
	return client.Do(req)
}

func (c *AtomGit) get(ctx context.Context, token, apiURL string, out any) error {
	resp, err := c.request(ctx, http.MethodGet, token, apiURL, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("atomgit api error: status %d: %s", resp.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// List endpoints return a plain JSON array; local fixtures wrap results in a
	// {"data": [...]} envelope. Accept both shapes transparently.
	if err := json.Unmarshal(body, out); err == nil {
		return nil
	} else {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &env) == nil && len(env.Data) > 0 {
			if e2 := json.Unmarshal(env.Data, out); e2 == nil {
				return nil
			} else {
				err = e2
			}
		}
		preview := body
		if len(preview) > 240 {
			preview = preview[:240]
		}
		return fmt.Errorf("atomgit api: failed to decode response: %w (body: %s)", err, string(preview))
	}
}

func (c *AtomGit) getRaw(ctx context.Context, token, apiURL string) ([]byte, int, error) {
	resp, err := c.request(ctx, http.MethodGet, token, apiURL, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func (c *AtomGit) post(ctx context.Context, token, apiURL string, body any) ([]byte, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.request(ctx, http.MethodPost, token, apiURL, strings.NewReader(string(raw)))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

func (c *AtomGit) delete(ctx context.Context, token, apiURL string) ([]byte, int, error) {
	resp, err := c.request(ctx, http.MethodDelete, token, apiURL, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}
