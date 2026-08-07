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
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.woodpecker-ci.org/woodpecker/v3/server/forge/atomgit/fixtures"
	"go.woodpecker-ci.org/woodpecker/v3/server/forge/types"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

func newHookRequest(event, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "/hook", bytes.NewBufferString(body))
	req.Header = http.Header{}
	req.Header.Set(hookEvent, event)
	return req
}

// newHookRequestGitCodeRaw builds a request with the exact header value
// AtomGit sends on GitCode-transport push deliveries, i.e. "Push Hook" (with a
// space and capitalized), to verify normalization to the internal "push" type.
func newHookRequestGitCodeRaw(event, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "/hook", bytes.NewBufferString(body))
	req.Header = http.Header{}
	req.Header.Set(hookEventGitCode, event)
	req.Header.Set(hookUserAgent, "git-gitcode-hook")
	return req
}

// newHookRequestGitCode builds a request carrying the AtomGit GitCode-transport
// X-GitCode-Event header and the git-gitcode-hook User-Agent.
func newHookRequestGitCode(event, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "/hook", bytes.NewBufferString(body))
	req.Header = http.Header{}
	req.Header.Set(hookEventGitCode, event)
	req.Header.Set(hookUserAgent, "git-gitcode-hook")
	return req
}

func TestParsePushHook(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequest(hookPush, fixtures.HookPush))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.EventPush, pipeline.Event)
	assert.Equal(t, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7", pipeline.Commit)
	assert.Equal(t, "master", pipeline.Branch)
	assert.Equal(t, "test_name/repo_name", repo.FullName)
	assert.Equal(t, []string{"file1.txt", "file2.txt"}, pipeline.ChangedFiles)
}

// TestParsePushHookGitCodeHeader verifies the GitCode-transport
// X-GitCode-Event header is accepted alongside the X-AtomGit-Event header,
// so hooks fire regardless of which transport delivered them.
func TestParsePushHookGitCodeHeader(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequestGitCode(hookPush, fixtures.HookPush))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.EventPush, pipeline.Event)
	assert.Equal(t, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7", pipeline.Commit)
}

// TestParsePushHookGitCodeRawHeader verifies the exact header value AtomGit
// sends on GitCode-transport push ("Push Hook", capitalized with a space) is
// normalized to the internal "push" event and parsed into a pipeline, instead
// of being ignored.
func TestParsePushHookGitCodeRawHeader(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequestGitCodeRaw("Push Hook", fixtures.HookPushGitCode))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.EventPush, pipeline.Event)
	assert.Equal(t, "main", pipeline.Branch)
	assert.Equal(t, "jetsung/testci", repo.FullName)
}

// TestParsePushHookGitCodeBody verifies the GitCode-transport push payload
// (user_username, git_http_url / git_ssh_url / web_url / homepage, gitcode.com
// domain) is parsed identically to the AtomGit-style payload: the clone URL,
// forge URL, branch and changed files are all derived correctly.
func TestParsePushHookGitCodeBody(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequest(hookPush, fixtures.HookPushGitCode))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.EventPush, pipeline.Event)
	assert.Equal(t, "8b1218282d9a5745ea816c280e00063fdc39eabf", pipeline.Commit)
	assert.Equal(t, "main", pipeline.Branch)
	assert.Equal(t, "jetsung/testci", repo.FullName)
	assert.Equal(t, "jetsung", repo.Owner)
	assert.Equal(t, "testci", repo.Name)
	// Clone URL must come from the git_http_url field.
	assert.Equal(t, "https://gitcode.com/jetsung/testci.git", repo.Clone)
	// Forge URL must come from web_url / homepage.
	assert.Equal(t, "https://gitcode.com/jetsung/testci", repo.ForgeURL)
	// Author should fall back to user_username when user_name is empty.
	assert.Equal(t, "jetsung", pipeline.Author)
	assert.Equal(t, []string{"README.md"}, pipeline.ChangedFiles)
}

// TestParsePushHookGitCodeUserAgent verifies that when no X-*-Event header is
// present, the git-gitcode-hook User-Agent alone still triggers a push parse.
func TestParsePushHookGitCodeUserAgent(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/hook", bytes.NewBufferString(fixtures.HookPushGitCode))
	req.Header = http.Header{}
	req.Header.Set(hookUserAgent, "git-gitcode-hook")
	repo, pipeline, err := parseHook(req)
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.EventPush, pipeline.Event)
}

func TestParseTagPushHook(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequest(hookTagPush, fixtures.HookTagPush))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.EventTag, pipeline.Event)
	assert.Equal(t, "refs/tags/v1.0.0", pipeline.Ref)
	assert.Equal(t, "82b3d5ae55f7080f1e6022629cdb57bfae7cccc7", pipeline.Commit)
}

func TestParseMergeRequestHook(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequest(hookMergeRequest, fixtures.HookMergeRequest))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.EventPull, pipeline.Event)
	assert.Equal(t, "refs/pull/1/head", pipeline.Ref)
	assert.Equal(t, "Add feature", pipeline.Title)
	assert.Equal(t, []string{"bug"}, pipeline.PullRequestLabels)
	assert.Equal(t, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7", pipeline.Commit)
}

func TestParseUnsupportedHook(t *testing.T) {
	req := newHookRequest("unknown_event", "{}")
	_, _, err := parseHook(req)
	assert.Error(t, err)
	var ignore *types.ErrIgnoreEvent
	assert.ErrorAs(t, err, &ignore)
}

func TestParsePushHookREQPayload(t *testing.T) {
	payload := `{
  "object_kind": "push",
  "event_name": "push",
  "before": "ceadf858db60318c9eea21e49d0f57af528160a1",
  "after": "fe99602109dce032b8aa22e236846008549429f9",
  "ref": "refs/heads/main",
  "checkout_sha": "fe99602109dce032b8aa22e236846008549429f9",
  "project_id": 10461881,
  "project": {
    "id": 10461881,
    "name": "testci",
    "web_url": "https://gitcode.com/jetsung/testci",
    "git_http_url": "https://gitcode.com/jetsung/testci.git",
    "namespace": "jetsung",
    "path_with_namespace": "jetsung/testci",
    "default_branch": "main"
  },
  "repository": {
    "name": "testci",
    "url": "git@gitcode.com:jetsung/testci.git",
    "git_http_url": "https://gitcode.com/jetsung/testci.git",
    "homepage": "https://gitcode.com/jetsung/testci"
  }
}`
	req := newHookRequestGitCodeRaw("Push Hook", payload)
	repo, pipeline, err := parseHook(req)
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, "jetsung", repo.Owner)
	assert.Equal(t, "testci", repo.Name)
	assert.Equal(t, "jetsung/testci", repo.FullName)
	assert.Equal(t, model.ForgeRemoteID("10461881"), repo.ForgeRemoteID)
}

// TestPipelineFromPushEmptyCommitsSynthesizesCommitURL verifies that when a
// push webhook carries no commits list (e.g. branch creation or force push),
// the commit link is synthesized from the repository page URL and the pushed
// SHA instead of being left empty (which would render as href="" / jump to the
// current page).
func TestPipelineFromPushEmptyCommitsSynthesizesCommitURL(t *testing.T) {
	hook := &pushHook{
		After:    "760ac6f2025bce95f6ba435b634f1fccc3218207",
		Ref:      "refs/heads/main",
		Project:  &repository{WebURL: "https://gitcode.com/jetsung/ci-demo"},
		Repository: &repository{
			WebURL:           "https://gitcode.com/jetsung/ci-demo",
			PathWithNamespace: "jetsung/ci-demo",
		},
		Commits:           []payloadCommit{},
		TotalCommitsCount: 0,
	}
	pipeline := pipelineFromPush(hook)
	assert.Equal(t, "https://gitcode.com/jetsung/ci-demo/commits/detail/760ac6f2025bce95f6ba435b634f1fccc3218207", pipeline.ForgeURL)
}

// TestPipelineFromTagMissingRepositoryURLFallsBackToProject verifies that a tag
// push webhook whose repository object lacks a page URL still produces a valid
// tag link by falling back to the project object's page URL.
func TestPipelineFromTagMissingRepositoryURLFallsBackToProject(t *testing.T) {
	hook := &tagPushHook{
		Ref:      "refs/tags/v1.0.0",
		After:    "82b3d5ae55f7080f1e6022629cdb57bfae7cccc7",
		Repository: &repository{Name: "ci-demo"}, // no page URL
		Project:    &repository{WebURL: "https://gitcode.com/jetsung/ci-demo"},
	}
	pipeline := pipelineFromTag(hook)
	assert.Equal(t, "https://gitcode.com/jetsung/ci-demo/-/tags/v1.0.0", pipeline.ForgeURL)
}

// TestPipelineFromMergeRequestMissingHTMLURLSynthesizesMRURL verifies that when
// a merge request webhook omits html_url (object_attributes.html_url), the MR
// link is synthesized from the project page URL and the MR IID rather than
// being left empty.
func TestPipelineFromMergeRequestMissingHTMLURLSynthesizesMRURL(t *testing.T) {
	hook := &mergeRequestHook{
		EventType: actionOpen,
		Project:   &repository{WebURL: "https://gitcode.com/jetsung/ci-demo"},
		ObjectAttributes: &pullRequest{
			IID:    id("1"),
			Title: "Add feature",
			// HTTPURL (html_url) intentionally omitted
		},
	}
	pipeline := pipelineFromMergeRequest(hook)
	assert.Equal(t, "https://gitcode.com/jetsung/ci-demo/-/merge_requests/1", pipeline.ForgeURL)
}

