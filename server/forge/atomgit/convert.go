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
	"fmt"
	"net/url"
	"strings"

	"go.woodpecker-ci.org/woodpecker/v3/server/model"
	"go.woodpecker-ci.org/woodpecker/v3/shared/utils"
)

// toUser converts a AtomGit user payload to a Woodpecker user.
func toUser(from *user) *model.User {
	avatar := expandAvatar(from.HTMLURL, from.AvatarURL)
	return &model.User{
		ForgeRemoteID: model.ForgeRemoteID(from.ID),
		Login:         from.Username,
		Email:         from.Email,
		Avatar:        avatar,
	}
}

// repoOwnerName splits a full name ("owner/repo") into owner and name.
func repoOwnerName(fullName string) (owner, name string) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return fullName, fullName
}

// extractOwnerNameFromURL attempts to parse owner and repository name from git/HTTP/web URLs.
func extractOwnerNameFromURL(rawURL string) (owner, name string) {
	if rawURL == "" {
		return "", ""
	}
	s := strings.TrimSuffix(rawURL, ".git")
	s = strings.TrimSuffix(s, "/")

	// Handle SSH format: git@domain.com:owner/repo
	if i := strings.Index(s, ":"); i >= 0 && !strings.Contains(s[:i], "/") {
		s = s[i+1:]
	} else if u, err := url.Parse(s); err == nil && u.Path != "" {
		s = u.Path
	}

	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1]
	}
	return "", ""
}

// toRepo converts a AtomGit repository payload to a Woodpecker repository.
func toRepo(from *repository) *model.Repo {
	// Webhook payloads may omit "full_name" and only carry
	// "path_with_namespace" (e.g. "jetsung/testci"), so fall back to it.
	fullName := from.FullName
	if fullName == "" && from.PathWithNamespace != "" {
		fullName = from.PathWithNamespace
	}
	owner, name := repoOwnerName(fullName)
	if owner == "" && from.Namespace != nil {
		owner = from.Namespace.Path
		name = from.Name
	}
	if owner == "" || name == "" {
		for _, rawURL := range []string{from.PageURL(), from.HTTPURL(), from.GitSSHURL, from.SSHURL, from.URL, from.WebURL, from.Homepage} {
			if o, n := extractOwnerNameFromURL(rawURL); o != "" && n != "" {
				owner, name = o, n
				break
			}
		}
	}
	if owner != "" && name != "" {
		from.FullName = fmt.Sprintf("%s/%s", owner, name)
	}

	avatar := expandAvatar(from.WebURL, from.AvatarURL)
	clone := from.GitURL
	if clone == "" && from.HTTPURL() != "" {
		clone = from.HTTPURL()
	}

	// Repository objects do not include `html_url`; the web page URL is carried
	// by `web_url` (or derivable from `http_url_to_repo` by stripping `.git`).
	forgeURL := from.PageURL()
	if forgeURL == "" && clone != "" {
		forgeURL = strings.TrimSuffix(clone, ".git")
	}

	// The API has no `has_pull_requests` field, so treat merge requests as
	// enabled so the Pull requests tab is shown.
	// Derive the SSH clone URL from the HTTP clone URL host.
	cloneSSH := sshURLFromClone(clone, owner, name)

	repo := &model.Repo{
		ForgeRemoteID: model.ForgeRemoteID(from.ID),
		Owner:         owner,
		Name:          name,
		FullName:      from.FullName,
		Avatar:        avatar,
		ForgeURL:      forgeURL,
		Clone:         clone,
		CloneSSH:      cloneSSH,
		Branch:        from.DefaultBranch,
		IsSCMPrivate:  !from.Public.Bool(),
		PREnabled:     true,
	}
	if perm := toPerm(from); perm != nil {
		repo.Perm = perm
	}
	return repo
}

// toPerm derives Woodpecker permissions from a AtomGit repository's permissions.
func toPerm(from *repository) *model.Perm {
	// AtomGit access levels: 10 Guest, 20 Reporter, 30 Developer, 40 Maintainer, 50 Owner.
	if from.Permissions == nil {
		// The repository listing endpoint (/user/repos) does not include a
		// permissions object. These repos are returned for the authenticated
		// user, so treat them as fully accessible (pull/push/admin) so the
		// add-repository page and permission sync can use them without a nil
		// pointer dereference upstream.
		return &model.Perm{
			Pull:  true,
			Push:  true,
			Admin: true,
		}
	}
	level := 0
	if from.Permissions.ProjectAccess != nil {
		level = from.Permissions.ProjectAccess.AccessLevel
	}
	if from.Permissions.GroupAccess != nil && from.Permissions.GroupAccess.AccessLevel > level {
		level = from.Permissions.GroupAccess.AccessLevel
	}
	return &model.Perm{
		Pull:  level >= 20,
		Push:  level >= 30,
		Admin: level >= 40,
	}
}

// toTeam converts a AtomGit namespace into a Woodpecker team.
func toTeam(from *namespace, link string) *model.Team {
	return &model.Team{
		Login:  from.Path,
		Avatar: expandAvatar(link, from.AvatarURL),
	}
}

// toOrg converts a AtomGit namespace into a Woodpecker org.
func toOrg(from *namespace) *model.Org {
	return &model.Org{
		Name:    from.Path,
		Private: from.VisibilityLevel != 0 && from.VisibilityLevel != 20,
	}
}

// pipelineFromPush converts a AtomGit push webhook into a Woodpecker pipeline.
func pipelineFromPush(hook *pushHook) *model.Pipeline {
	avatar := expandAvatar(hook.Repository.PageURL(), hook.UserAvatar)
	author := hook.UserName
	if author == "" {
		author = hook.UserUsername
	}
	if author == "" {
		author = hook.UserEmail
	}

	var message string
	if len(hook.Commits) > 0 {
		message = hook.Commits[0].Message
	}
	if message == "" {
		message = fmt.Sprintf("push %s", hook.After)
	}
	// Always synthesize the commit URL from the repository page URL and the
	// pushed SHA, using the {repo}/commits/detail/{sha} scheme that matches the
	// webhook's commits[].url. The webhook value is not trusted directly so the
	// format stays consistent regardless of what the payload carries.
	var link string
	if hook.After != "" {
		if page := hook.Repository.PageURL(); page != "" {
			link = fmt.Sprintf("%s/commits/detail/%s", page, hook.After)
		}
	}

	return &model.Pipeline{
		Event:        model.EventPush,
		Commit:       hook.After,
		Ref:          hook.Ref,
		ForgeURL:     link,
		Branch:       strings.TrimPrefix(hook.Ref, "refs/heads/"),
		Message:      message,
		Avatar:       avatar,
		Author:       author,
		Email:        hook.UserEmail,
		Timestamp:    0,
		Sender:       author,
		ChangedFiles: getChangedFilesFromPushHook(hook),
	}
}

// pipelineFromTag converts a AtomGit tag push webhook into a Woodpecker pipeline.
func pipelineFromTag(hook *tagPushHook) *model.Pipeline {
	pageURL := hook.Repository.PageURL()
	if pageURL == "" && hook.Project != nil {
		pageURL = hook.Project.PageURL()
	}
	avatar := expandAvatar(pageURL, hook.UserAvatar)
	author := hook.UserName
	if author == "" {
		author = hook.UserUsername
	}
	if author == "" {
		author = hook.UserEmail
	}
	ref := strings.TrimPrefix(hook.Ref, "refs/tags/")

	return &model.Pipeline{
		Event:     model.EventTag,
		Commit:    hook.After,
		Ref:       fmt.Sprintf("refs/tags/%s", ref),
		ForgeURL:  fmt.Sprintf("%s/-/tags/%s", pageURL, ref),
		Message:   fmt.Sprintf("created tag %s", ref),
		Avatar:    avatar,
		Author:    author,
		Email:     hook.UserEmail,
		Timestamp: 0,
		Sender:    author,
	}
}

// pipelineFromMergeRequest converts a AtomGit merge request webhook into a Woodpecker pipeline.
func pipelineFromMergeRequest(hook *mergeRequestHook) *model.Pipeline {
	pr := hook.ObjectAttributes
	pageURL := hook.Project.PageURL()
	avatar := expandAvatar(pageURL, "")
	if pr.Author != nil {
		avatar = expandAvatar(pageURL, pr.Author.AvatarURL)
	}

	event := model.EventPull
	switch hook.EventType {
	case actionClose, actionMerge:
		event = model.EventPullClosed
	case actionUpdate:
		event = model.EventPull
	}

	base := pr.TargetBranch
	head := pr.SourceBranch

	commitSHA := ""
	if pr.LastCommit != nil {
		commitSHA = pr.LastCommit.ID
	}

	// object_attributes.html_url is absent on some merge request deliveries, so
	// fall back to a link synthesized from the project page URL and the MR IID;
	// otherwise the UI link is empty and jumps to the current page.
	forgeURL := pr.HTTPURL
	if forgeURL == "" {
		forgeURL = fmt.Sprintf("%s/-/merge_requests/%s", pageURL, fmt.Sprint(pr.IID))
	}

	pipeline := &model.Pipeline{
		Event:    event,
		Commit:   commitSHA,
		ForgeURL: forgeURL,
		Ref:      fmt.Sprintf("refs/pull/%s/head", pr.IID),
		Branch:   base,
		Message:  pr.Title,
		Avatar:   avatar,
		Author:   authorLogin(pr.Author),
		Sender:   authorLogin(hook.User),
		Email:    authorEmail(pr.Author),
		Title:    pr.Title,
		Refspec:  fmt.Sprintf("%s:%s", head, base),
		FromFork: pr.SourceRepo != nil && pr.TargetRepo != nil && pr.SourceRepo.ID != pr.TargetRepo.ID,
	}
	if labels := convertLabels(hook.Labels); len(labels) > 0 {
		pipeline.PullRequestLabels = labels
	} else if labels := convertLabels(pr.Labels); len(labels) > 0 {
		pipeline.PullRequestLabels = labels
	}
	if pr.Milestone != nil {
		pipeline.PullRequestMilestone = pr.Milestone.Title
	}
	pipeline.PullRequestDraft = pr.Draft
	return pipeline
}

func authorLogin(u *user) string {
	if u == nil {
		return ""
	}
	return u.Username
}

func authorEmail(u *user) string {
	if u == nil {
		return ""
	}
	return u.Email
}

func convertLabels(from []label) []string {
	labels := make([]string, 0, len(from))
	for _, label := range from {
		labels = append(labels, label.Name)
	}
	return labels
}

// getChangedFilesFromPushHook collects changed files from a push webhook payload.
func getChangedFilesFromPushHook(hook *pushHook) []string {
	files := make([]string, 0)
	for _, c := range hook.Commits {
		files = append(files, c.Added...)
		files = append(files, c.Modified...)
		files = append(files, c.Removed...)
	}
	return utils.DeduplicateStrings(files)
}

// HTTPURL returns the HTTP clone URL of a repository. AtomGit returns the
// clone URL under either http_url_to_repo or git_http_url, with web_url as a
// last resort; the first present value wins.
func (r *repository) HTTPURL() string {
	if r.GitURL != "" {
		return r.GitURL
	}
	if r.GitHTTPURL != "" {
		return r.GitHTTPURL
	}
	return r.WebURL
}

// PageURL returns the repository's web page URL. AtomGit returns it under one
// of html_url / web_url / homepage / url; the first present value wins.
func (r *repository) PageURL() string {
	switch {
	case r.HTMLURL != "":
		return r.HTMLURL
	case r.WebURL != "":
		return r.WebURL
	case r.Homepage != "":
		return r.Homepage
	case r.URL != "":
		return r.URL
	}
	return ""
}

// sshURLFromClone derives the SSH clone URL from the HTTP clone URL's host,
// so both clone methods point at the same forge host.
func sshURLFromClone(clone, owner, name string) string {
	if clone == "" || owner == "" || name == "" {
		return ""
	}
	host := clone
	if i := strings.Index(clone, "://"); i >= 0 {
		host = clone[i+len("://"):]
	}
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return fmt.Sprintf("git@%s:%s/%s.git", host, owner, name)
}

// knownCDNHosts maps avatar CDN hosts to their forge referrer hosts. Avatar
// URLs on these CDNs enforce hotlink protection (防盗链), so they are routed
// through the Woodpecker avatar proxy with the matching Referer header.
var knownCDNHosts = map[string]string{
	"cdn-img.gitcode.com": "https://gitcode.com",
	"cdn-img.atomgit.com": "https://atomgit.com",
}

// expandAvatar resolves a possibly-relative avatar URL against the base URL.
func expandAvatar(repo, rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	aURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if aURL.IsAbs() {
		// CDN URLs with hotlink protection need to go through the
		// Woodpecker avatar proxy so the server sets the correct
		// Referer header when fetching the image.
		if forgeURL, ok := knownCDNHosts[aURL.Host]; ok {
			q := url.Values{}
			q.Set("url", rawURL)
			q.Set("referer", forgeURL)
			return fmt.Sprintf("/api/avatar-proxy?%s", q.Encode())
		}
		return aURL.String()
	}
	bURL, err := url.Parse(repo)
	if err != nil {
		return rawURL
	}
	return bURL.ResolveReference(aURL).String()
}
