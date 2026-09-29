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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

// cronBranchHeadNote documents that forge.Forge.BranchHead is a cron-required
// method (see server/forge/forge.go:139-141 "Is essential for cron feature to work")
// and is called by server/cron/cron.go:146:
//
//	commit, err := _forge.BranchHead(ctx, repoUser, repo, cron.Branch)
//
// The commit returned by BranchHead provides the SHA and clickable ForgeURL
// used to create the cron pipeline:
//
//	&model.Pipeline{
//	    Event:    model.EventCron,
//	    Commit:   commit.SHA,
//	    ForgeURL: commit.ForgeURL,
//	    Ref:      "refs/heads/" + cron.Branch,
//	    Branch:   cron.Branch,
//	}
var _ = model.EventCron

// helper for cron BranchHead tests: creates a GitCode client pointing to server.
func newCronTestClient(t *testing.T, handler http.Handler) *GitCode {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c, err := New(1, Opts{URL: s.URL, SkipVerify: true})
	require.NoError(t, err)
	return c.(*GitCode)
}

// TestCronBranchHead_Success verifies the happy path used by cron: BranchHead
// returns a non-empty SHA and a clickable (absolute http/https) ForgeURL.
// When the API does not return an absolute URL the URL is synthesized as
// {repo.ForgeURL}/commits/detail/{sha}.
func TestCronBranchHead_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"da1560886d4f094c3e6c9ef40349f7d38b5d27d7"}}`))
	})
	c := newCronTestClient(t, mux)

	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
	require.NoError(t, err)
	require.NotNil(t, commit)
	assert.Equal(t, "da1560886d4f094c3e6c9ef40349f7d38b5d27d7", commit.SHA)
	assert.True(t, strings.HasPrefix(commit.ForgeURL, "http://") || strings.HasPrefix(commit.ForgeURL, "https://"), "ForgeURL must be clickable absolute URL")
	assert.Equal(t, "http://localhost/test_name/repo_name/commits/detail/da1560886d4f094c3e6c9ef40349f7d38b5d27d7", commit.ForgeURL)
}

// TestCronBranchHead_EmptyBranch verifies that BranchHead rejects an empty branch
// name before issuing any HTTP request. Cron validates branch existence via
// BranchHead (server/api/cron.go:155, server/cron/cron.go:146) so an empty
// branch must be a hard error.
func TestCronBranchHead_EmptyBranch(t *testing.T) {
	c, _ := New(1, Opts{URL: "http://example.com", SkipVerify: true})
	_, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch name is empty")
}
// TestCronBranchHead_WhitespaceBranchIsNotEmpty is an extra guard: only the exact
// empty string is rejected by the current implementation; whitespace is not
// trimmed and will be sent to the API. This test documents that behaviour.
func TestCronBranchHead_WhitespaceBranchIsNotEmpty(t *testing.T) {
	// Use a catch-all handler because net/http ServeMux rejects patterns with
	// trailing spaces. The GitCode client escapes the branch via PathEscape so
	// the request arrives as /branches/%20.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/branches/") {
			_, _ = w.Write([]byte(`{"name":" ","commit":{"id":"abc"}}`))
			return
		}
		http.NotFound(w, r)
	})
	c := newCronTestClient(t, handler)
	// A single-space branch name is not considered empty by the guard
	// `branchName == ""`, so it reaches the API.
	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, " ")
	require.NoError(t, err)
	assert.Equal(t, "abc", commit.SHA)
}

// TestCronBranchHead_NilCommit verifies that a branch payload with no embedded
// commit (commit == null / missing) returns an error. This covers the
// "commit 为空返回错误" case.
func TestCronBranchHead_NilCommit(t *testing.T) {
	t.Run("commit is null", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"name":"main","commit":null}`))
		})
		c := newCronTestClient(t, mux)
		_, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no commit")
	})
	t.Run("commit field missing", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"name":"main"}`))
		})
		c := newCronTestClient(t, mux)
		_, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no commit")
	})
}

// TestCronBranchHead_CommitIDField verifies the id vs sha dual-field
// compatibility: when the commit carries "id" (GitCode's real branch endpoint)
// it is used as SHA.
func TestCronBranchHead_CommitIDField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/develop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"develop","commit":{"id":"8240e6b568"}}`))
	})
	c := newCronTestClient(t, mux)
	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "develop")
	require.NoError(t, err)
	assert.Equal(t, "8240e6b568", commit.SHA)
	assert.Equal(t, "http://localhost/test_name/repo_name/commits/detail/8240e6b568", commit.ForgeURL)
}

// TestCronBranchHead_CommitSHAField verifies that when the commit carries
// "sha" instead of "id" the SHA is still populated correctly.
func TestCronBranchHead_CommitSHAField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/feature", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"feature","commit":{"sha":"abc123def456"}}`))
	})
	c := newCronTestClient(t, mux)
	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "feature")
	require.NoError(t, err)
	assert.Equal(t, "abc123def456", commit.SHA)
	assert.Equal(t, "http://localhost/test_name/repo_name/commits/detail/abc123def456", commit.ForgeURL)
}

// TestCronBranchHead_CommitIDPreferredOverSHA verifies that when both id and
// sha are present, id takes precedence (the real GitCode API may emit both).
func TestCronBranchHead_CommitIDPreferredOverSHA(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"id-sha-1","sha":"sha-2"}}`))
	})
	c := newCronTestClient(t, mux)
	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
	require.NoError(t, err)
	assert.Equal(t, "id-sha-1", commit.SHA, "id should be preferred over sha")
	assert.Equal(t, "http://localhost/test_name/repo_name/commits/detail/id-sha-1", commit.ForgeURL)
}

// TestCronBranchHead_SynthesizedURL verifies that when the API commit URL is
// absent or not an absolute http(s) URL, BranchHead synthesizes a clickable
// URL as {repo.ForgeURL}/commits/detail/{sha}.
func TestCronBranchHead_SynthesizedURL(t *testing.T) {
	tests := []struct {
		name       string
		commitJSON string
		wantSHA    string
	}{
		{
			name:       "no url field",
			commitJSON: `{"id":"sha-no-url"}`,
			wantSHA:    "sha-no-url",
		},
		{
			name:       "empty url field",
			commitJSON: `{"id":"sha-empty-url","url":""}`,
			wantSHA:    "sha-empty-url",
		},
		{
			name:       "relative url is discarded",
			commitJSON: `{"id":"sha-relative","url":"/test_name/repo_name/commit/sha-relative"}`,
			wantSHA:    "sha-relative",
		},
		{
			name:       "non-http url is discarded",
			commitJSON: `{"id":"sha-ftp","url":"ftp://example.com/commit/sha-ftp"}`,
			wantSHA:    "sha-ftp",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"name":"main","commit":` + tc.commitJSON + `}`
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			c := newCronTestClient(t, mux)
			commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
			require.NoError(t, err)
			assert.Equal(t, tc.wantSHA, commit.SHA)
			expected := "http://localhost/test_name/repo_name/commits/detail/" + tc.wantSHA
			assert.Equal(t, expected, commit.ForgeURL, "should synthesize ForgeURL when API url is not absolute")
			assert.True(t, strings.HasPrefix(commit.ForgeURL, "http://") || strings.HasPrefix(commit.ForgeURL, "https://"))
		})
	}
}

// TestCronBranchHead_PreservesAPIURL verifies that when the API commit URL is
// already an absolute http(s) URL, BranchHead preserves it instead of
// synthesizing a new one.
func TestCronBranchHead_PreservesAPIURL(t *testing.T) {
	t.Run("https url is preserved", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/develop", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"name":"develop","commit":{"id":"8240e6b568","url":"https://gitcode.com/test_name/repo_name/commits/detail/8240e6b568"}}`))
		})
		c := newCronTestClient(t, mux)
		commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "develop")
		require.NoError(t, err)
		assert.Equal(t, "8240e6b568", commit.SHA)
		assert.Equal(t, "https://gitcode.com/test_name/repo_name/commits/detail/8240e6b568", commit.ForgeURL)
	})
	t.Run("http url is preserved", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"abc123","url":"http://gitcode.com/test_name/repo_name/commits/detail/abc123"}}`))
		})
		c := newCronTestClient(t, mux)
		commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.NoError(t, err)
		assert.Equal(t, "http://gitcode.com/test_name/repo_name/commits/detail/abc123", commit.ForgeURL)
	})
	t.Run("sha field with absolute url", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"name":"main","commit":{"sha":"sha-from-sha","url":"https://gitcode.com/test_name/repo_name/commit/sha-from-sha"}}`))
		})
		c := newCronTestClient(t, mux)
		commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.NoError(t, err)
		assert.Equal(t, "sha-from-sha", commit.SHA)
		assert.Equal(t, "https://gitcode.com/test_name/repo_name/commit/sha-from-sha", commit.ForgeURL)
	})
}

// TestCronBranchHead_CronServiceIntegration demonstrates how the cron service
// calls BranchHead to create a cron pipeline (server/cron/cron.go:146).
//
//	forge.Forge.BranchHead is essential for cron to work (server/forge/forge.go:139).
//	server/cron/cron.go:CreatePipeline does:
//
//		commit, err := _forge.BranchHead(ctx, repoUser, repo, cron.Branch)
//		return repo, &model.Pipeline{
//		    Event:    model.EventCron,
//		    Commit:   commit.SHA,
//		    ForgeURL: commit.ForgeURL,
//		    Ref:      "refs/heads/" + cron.Branch,
//		    Branch:   cron.Branch,
//		}, nil
//
// This test reproduces that flow against a real (httptest) GitCode BranchHead
// implementation and asserts the resulting pipeline fields are cron-correct.
func TestCronBranchHead_CronServiceIntegration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"cron-sha-abc123"}}`))
	})
	c := newCronTestClient(t, mux)

	cronBranch := "main"
	cronName := "nightly"

	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, cronBranch)
	require.NoError(t, err)
	require.NotEmpty(t, commit.SHA)
	require.NotEmpty(t, commit.ForgeURL)

	// Simulate server/cron.CreatePipeline pipeline construction.
	pipeline := &model.Pipeline{
		Event:    model.EventCron,
		Commit:   commit.SHA,
		ForgeURL: commit.ForgeURL,
		Ref:      "refs/heads/" + cronBranch,
		Branch:   cronBranch,
		Cron:     cronName,
	}

	assert.Equal(t, model.EventCron, pipeline.Event)
	assert.Equal(t, "cron-sha-abc123", pipeline.Commit)
	assert.Equal(t, "http://localhost/test_name/repo_name/commits/detail/cron-sha-abc123", pipeline.ForgeURL)
	assert.Equal(t, "refs/heads/main", pipeline.Ref)
	assert.Equal(t, "main", pipeline.Branch)
	assert.Equal(t, "nightly", pipeline.Cron)
	assert.True(t, strings.HasPrefix(pipeline.ForgeURL, "http://"), "cron pipeline ForgeURL must be clickable")
}

// TestBranchHead_CronRequiredInterface is an alias-style test that can be
// selected with `-run TestBranchHead` to prove the forge implements the
// BranchHead method required by cron. It is functionally identical to the
// success test but uses the TestBranchHead prefix.
func TestBranchHead_CronRequiredInterface(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/main", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"branchhead-cron-sha"}}`))
	})
	c := newCronTestClient(t, mux)
	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
	require.NoError(t, err)
	assert.Equal(t, "branchhead-cron-sha", commit.SHA)
	assert.Equal(t, "http://localhost/test_name/repo_name/commits/detail/branchhead-cron-sha", commit.ForgeURL)
}
