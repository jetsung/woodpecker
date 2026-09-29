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

package gitcode

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.woodpecker-ci.org/woodpecker/v3/server/forge/gitcode/fixtures"
	"go.woodpecker-ci.org/woodpecker/v3/server/forge/types"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

// Test_parseHook mirrors github/parse_test.go:Test_parseHook structure and
// covers every parseHook branch for gitcode: push, forced push, tag via push,
// tag_push, release, prerelease, MR open/reopen/update/close/merge, unsupported
// MR action, deployment, unknown hook, and Release Draft.
func Test_parseHook(t *testing.T) {
	t.Run("ignore unsupported hook events", func(t *testing.T) {
		req := newHookRequest("unknown_event", "{}")
		repo, pipeline, err := parseHook(req)
		assert.Nil(t, repo)
		assert.Nil(t, pipeline)
		var ignore *types.ErrIgnoreEvent
		assert.ErrorAs(t, err, &ignore)
	})

	t.Run("unknown hook is ignored", func(t *testing.T) {
		req := newHookRequest("issues", `{"object_kind":"issue"}`)
		_, _, err := parseHook(req)
		var ignore *types.ErrIgnoreEvent
		require.ErrorAs(t, err, &ignore)
		assert.Equal(t, "issues", ignore.Event)
	})

	t.Run("push hook", func(t *testing.T) {
		req := newHookRequest(hookPush, fixtures.HookPush)
		repo, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, repo)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventPush, pipeline.Event)
		assert.Equal(t, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7", pipeline.Commit)
		assert.Equal(t, "refs/heads/master", pipeline.Ref)
		assert.Equal(t, "master", pipeline.Branch)
		assert.Equal(t, "test_name/repo_name", repo.FullName)
		assert.NotEmpty(t, pipeline.ForgeURL)
		assert.Contains(t, pipeline.ForgeURL, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7")
		assert.Equal(t, []string{"file1.txt", "file2.txt"}, pipeline.ChangedFiles)
	})

	t.Run("forced push hook", func(t *testing.T) {
		// Force push: before is rewritten history, commits may be empty.
		// GitCode still delivers a push event; Commit must be After and Branch derived from Ref.
		forcedPayload := `{
  "object_kind": "push",
  "event_name": "push",
  "before": "95790bf891e76feeb30fa2fcc762bd98c1e28ad9",
  "after": "f7220f1f753260bf6f1c533357c70213e9fd4abe",
  "ref": "refs/heads/master",
  "checkout_sha": "f7220f1f753260bf6f1c533357c70213e9fd4abe",
  "user_id": 4,
  "user_name": "John Doe",
  "user_email": "john@example.com",
  "user_avatar": "https://gitcode.com/avatar.png",
  "project_id": 15,
  "project": {
    "id": 15,
    "name": "repo_name",
    "path_with_namespace": "test_name/repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://gitcode.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://gitcode.com/test_name/repo_name"
  },
  "repository": {
    "id": 15,
    "name": "repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://gitcode.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://gitcode.com/test_name/repo_name"
  },
  "commits": [],
  "total_commits_count": 0
}`
		req := newHookRequest(hookPush, forcedPayload)
		repo, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, repo)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventPush, pipeline.Event)
		assert.Equal(t, "f7220f1f753260bf6f1c533357c70213e9fd4abe", pipeline.Commit)
		assert.Equal(t, "refs/heads/master", pipeline.Ref)
		assert.Equal(t, "master", pipeline.Branch)
	})

	t.Run("tag hook", func(t *testing.T) {
		req := newHookRequest(hookTagPush, fixtures.HookTagPush)
		repo, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, repo)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventTag, pipeline.Event)
		assert.Equal(t, "refs/tags/v1.0.0", pipeline.Ref)
		assert.Equal(t, "82b3d5ae55f7080f1e6022629cdb57bfae7cccc7", pipeline.Commit)
		assert.Contains(t, pipeline.ForgeURL, "/-/tags/v1.0.0")
		assert.Equal(t, "created tag v1.0.0", pipeline.Message)
	})

	t.Run("push tag ref enters tag pipeline", func(t *testing.T) {
		req := newHookRequest(hookPush, fixtures.HookPushTag)
		repo, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, repo)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventTag, pipeline.Event)
		assert.Equal(t, "refs/tags/v1.0.0", pipeline.Ref)
		assert.Equal(t, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7", pipeline.Commit)
		assert.Contains(t, pipeline.ForgeURL, "/-/tags/v1.0.0")
	})

	t.Run("release hook released", func(t *testing.T) {
		req := newHookRequest("Release Hook", fixtures.HookRelease)
		repo, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, repo)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventRelease, pipeline.Event)
		assert.Equal(t, "refs/tags/v1.2.3", pipeline.Ref)
		assert.Equal(t, "v1.2.3", pipeline.TagTitle)
		assert.Equal(t, "main", pipeline.Branch)
		require.NotNil(t, pipeline.Release)
		assert.Equal(t, "v1.2.3", pipeline.Release.Title)
		assert.False(t, pipeline.Release.IsPrerelease)
		assert.Contains(t, pipeline.ForgeURL, "/-/releases/v1.2.3")
	})

	t.Run("release hook prerelease", func(t *testing.T) {
		prereleasePayload := strings.Replace(fixtures.HookRelease, `"prerelease": false`, `"prerelease": true`, 1)
		req := newHookRequest("Release Hook", prereleasePayload)
		repo, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, repo)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventRelease, pipeline.Event)
		assert.Equal(t, "refs/tags/v1.2.3", pipeline.Ref)
		assert.Equal(t, "v1.2.3", pipeline.TagTitle)
		require.NotNil(t, pipeline.Release)
		assert.Equal(t, "v1.2.3", pipeline.Release.Title)
		assert.True(t, pipeline.Release.IsPrerelease)
	})

	t.Run("Release Draft is ignored", func(t *testing.T) {
		req := newHookRequest("Release Hook", fixtures.HookReleaseDraft)
		_, _, err := parseHook(req)
		require.Error(t, err)
		var ignore *types.ErrIgnoreEvent
		require.ErrorAs(t, err, &ignore)
		assert.Equal(t, string(model.EventRelease), ignore.Event)
	})

	t.Run("MR open hook", func(t *testing.T) {
		req := newHookRequest(hookMergeRequest, fixtures.HookMergeRequest)
		repo, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, repo)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventPull, pipeline.Event)
		assert.Equal(t, "refs/pull/1/head", pipeline.Ref)
		assert.Equal(t, "master", pipeline.Branch)
		assert.Equal(t, "Add feature", pipeline.Title)
		assert.Equal(t, "Add feature", pipeline.Message)
		assert.Equal(t, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7", pipeline.Commit)
		assert.Equal(t, "feature:master", pipeline.Refspec)
		assert.Contains(t, pipeline.ForgeURL, "/-/merge_requests/1")
	})

	t.Run("MR reopen hook", func(t *testing.T) {
		payload := strings.Replace(fixtures.HookMergeRequest, "merge_request_open", "merge_request_reopen", 1)
		req := newHookRequest(hookMergeRequest, payload)
		_, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventPull, pipeline.Event)
		assert.Equal(t, "refs/pull/1/head", pipeline.Ref)
	})

	t.Run("MR update hook", func(t *testing.T) {
		payload := strings.Replace(fixtures.HookMergeRequest, "merge_request_open", "merge_request_update", 1)
		req := newHookRequest(hookMergeRequest, payload)
		_, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventPull, pipeline.Event)
		assert.Equal(t, "refs/pull/1/head", pipeline.Ref)
	})

	t.Run("MR close hook", func(t *testing.T) {
		payload := strings.Replace(fixtures.HookMergeRequest, "merge_request_open", "merge_request_close", 1)
		req := newHookRequest(hookMergeRequest, payload)
		_, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventPullClosed, pipeline.Event)
		assert.Equal(t, "refs/pull/1/head", pipeline.Ref)
	})

	t.Run("MR merge hook", func(t *testing.T) {
		payload := strings.Replace(fixtures.HookMergeRequest, "merge_request_open", "merge_request_merge", 1)
		req := newHookRequest(hookMergeRequest, payload)
		_, pipeline, err := parseHook(req)
		require.NoError(t, err)
		require.NotNil(t, pipeline)
		assert.Equal(t, model.EventPullClosed, pipeline.Event)
		assert.Equal(t, "refs/pull/1/head", pipeline.Ref)
	})

	t.Run("MR unsupported action is ignored ErrIgnoreEvent", func(t *testing.T) {
		payload := `{"object_kind":"merge_request","event_name":"merge_request_labeled","project":{"id":15,"full_name":"test_name/repo_name","html_url":"https://gitcode.com/test_name/repo_name"},"object_attributes":{"id":99,"iid":1,"target_branch":"master","source_branch":"feature","title":"Add feature","state":"opened"}}`
		req := newHookRequest(hookMergeRequest, payload)
		_, _, err := parseHook(req)
		require.Error(t, err)
		var ignore *types.ErrIgnoreEvent
		require.ErrorAs(t, err, &ignore)
		assert.True(t, errors.Is(err, &types.ErrIgnoreEvent{}))
	})

	t.Run("deployment hook is ignored", func(t *testing.T) {
		req := newHookRequest("deployment", "{}")
		_, _, err := parseHook(req)
		require.Error(t, err)
		var ignore *types.ErrIgnoreEvent
		require.ErrorAs(t, err, &ignore)
		assert.Equal(t, "deployment", ignore.Event)
		assert.True(t, errors.Is(err, &types.ErrIgnoreEvent{}))
	})

	t.Run("Deployment Hook header is ignored", func(t *testing.T) {
		req := newHookRequest("Deployment Hook", "{}")
		_, _, err := parseHook(req)
		require.Error(t, err)
		var ignore *types.ErrIgnoreEvent
		require.ErrorAs(t, err, &ignore)
		assert.Equal(t, "deployment", ignore.Event)
	})

	// Additional edge: ensure TagTitle and Release are empty for non-release events
	t.Run("push does not set TagTitle or Release", func(t *testing.T) {
		req := newHookRequest(hookPush, fixtures.HookPush)
		_, pipeline, err := parseHook(req)
		require.NoError(t, err)
		assert.Empty(t, pipeline.TagTitle)
		assert.Nil(t, pipeline.Release)
	})

	t.Run("tag does not set Release", func(t *testing.T) {
		req := newHookRequest(hookTagPush, fixtures.HookTagPush)
		_, pipeline, err := parseHook(req)
		require.NoError(t, err)
		assert.Empty(t, pipeline.TagTitle)
		assert.Nil(t, pipeline.Release)
	})
}
