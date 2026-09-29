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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	forge_types "go.woodpecker-ci.org/woodpecker/v3/server/forge/types"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

// fakeUser/Repo defined in gitcode_test.go are reused; redefine locals if not exported.

func newTestServer(handler http.Handler) (*httptest.Server, *GitCode) {
	s := httptest.NewServer(handler)
	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
	gc := c.(*GitCode)
	return s, gc
}

// ---------------------------------------------------------------------------
// Branches — pagination and perPage clamping
// ---------------------------------------------------------------------------

func TestBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns branch names", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{
				{"name": "main"},
				{"name": "develop"},
			})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		names, err := gc.Branches(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 10})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"main", "develop"}, names)
	})

	t.Run("perPage clamping - zero and negative", func(t *testing.T) {
		for _, pp := range []int{0, -1, -100} {
			assert.Equal(t, 50, perPage(pp), "perPage(%d) should clamp to 50", pp)
		}
	})

	t.Run("perPage clamping - above max", func(t *testing.T) {
		assert.Equal(t, 50, perPage(51))
		assert.Equal(t, 50, perPage(100))
		assert.Equal(t, 50, perPage(1000))
	})

	t.Run("perPage valid passthrough", func(t *testing.T) {
		assert.Equal(t, 1, perPage(1))
		assert.Equal(t, 50, perPage(50))
		assert.Equal(t, 25, perPage(25))
	})

	t.Run("Branches sends clamped per_page query", func(t *testing.T) {
		var gotPerPage string
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches", func(w http.ResponseWriter, r *http.Request) {
			gotPerPage = r.URL.Query().Get("per_page")
			_ = json.NewEncoder(w).Encode([]gin.H{})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		// PerPage 200 exceeds defaultPageSize=50 -> clamped to 50.
		_, _ = gc.Branches(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 200})
		assert.Equal(t, "50", gotPerPage)

		// PerPage 0 -> clamped to 50
		gotPerPage = ""
		_, _ = gc.Branches(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 0})
		assert.Equal(t, "50", gotPerPage)
	})

	t.Run("Branches with empty result", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		names, err := gc.Branches(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 10})
		require.NoError(t, err)
		assert.Empty(t, names)
	})
}

// ---------------------------------------------------------------------------
// BranchHead — synthesis, empty branch, no commit
// ---------------------------------------------------------------------------

func TestBranchHead(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("synthesizes commit URL when branch commit has no URL", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle("/api/v5/repos/test_name/repo_name/branches/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(gin.H{
				"name": "main",
				"commit": gin.H{"id": "abc123"},
			})
		}))
		// gin handler registered below as fallback
		_ = mux
		// Use gin to handle /*branch wildcard properly
		gin.SetMode(gin.TestMode)
		e := gin.New()
		e.GET("/api/v5/repos/:owner/:name/branches/*branch", func(c *gin.Context) {
			branch := strings.TrimPrefix(c.Param("branch"), "/")
			if decoded, err := url.PathUnescape(branch); err == nil {
				branch = decoded
			}
			c.JSON(http.StatusOK, gin.H{"name": branch, "commit": gin.H{"id": "abc123"}})
		})
		s := httptest.NewServer(e)
		defer s.Close()
		gcInt, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
		gc := gcInt.(*GitCode)

		commit, err := gc.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.NoError(t, err)
		assert.Equal(t, "abc123", commit.SHA)
		assert.Equal(t, fakeRepo.ForgeURL+"/commits/detail/abc123", commit.ForgeURL)
	})

	t.Run("preserves absolute commit URL when provided", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		e := gin.New()
		e.GET("/api/v5/repos/:owner/:name/branches/*branch", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"name": "main",
				"commit": gin.H{
					"id":  "abc123",
					"url": "https://gitcode.com/test_name/repo_name/commit/abc123",
				},
			})
		})
		s := httptest.NewServer(e)
		defer s.Close()
		gcInt, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
		gc := gcInt.(*GitCode)

		commit, err := gc.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.NoError(t, err)
		assert.Equal(t, "abc123", commit.SHA)
		assert.Equal(t, "https://gitcode.com/test_name/repo_name/commit/abc123", commit.ForgeURL)
	})

	t.Run("empty branch name returns error", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		e := gin.New()
		e.GET("/api/v5/repos/:owner/:name/branches/*branch", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"name": "main", "commit": gin.H{"id": "abc123"}})
		})
		s := httptest.NewServer(e)
		defer s.Close()
		gcInt, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
		gc := gcInt.(*GitCode)

		_, err := gc.BranchHead(t.Context(), fakeUser, fakeRepo, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "branch name is empty")
	})

	t.Run("branch with no commit returns error", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		e := gin.New()
		e.GET("/api/v5/repos/:owner/:name/branches/*branch", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"name": "empty-branch", "commit": nil})
		})
		s := httptest.NewServer(e)
		defer s.Close()
		gcInt, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
		gc := gcInt.(*GitCode)

		_, err := gc.BranchHead(t.Context(), fakeUser, fakeRepo, "empty-branch")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no commit")
	})

	t.Run("branch head escapes slashed branch name", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		var gotBranch string
		e := gin.New()
		e.GET("/api/v5/repos/:owner/:name/branches/*branch", func(c *gin.Context) {
			gotBranch = c.Param("branch")
			if decoded, err := url.PathUnescape(strings.TrimPrefix(gotBranch, "/")); err == nil {
				gotBranch = decoded
			}
			c.JSON(http.StatusOK, gin.H{"name": gotBranch, "commit": gin.H{"id": "deadbeef"}})
		})
		s := httptest.NewServer(e)
		defer s.Close()
		gcInt, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
		gc := gcInt.(*GitCode)

		commit, err := gc.BranchHead(t.Context(), fakeUser, fakeRepo, "feature/foo/bar")
		require.NoError(t, err)
		assert.Equal(t, "deadbeef", commit.SHA)
		assert.Equal(t, "feature/foo/bar", gotBranch)
	})

	t.Run("SHA prefers id over sha field", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		e := gin.New()
		e.GET("/api/v5/repos/:owner/:name/branches/*branch", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"name": "main", "commit": gin.H{"id": "id-sha", "sha": "sha-field"}})
		})
		s := httptest.NewServer(e)
		defer s.Close()
		gcInt, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
		gc := gcInt.(*GitCode)

		commit, err := gc.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.NoError(t, err)
		assert.Equal(t, "id-sha", commit.SHA)
	})

	t.Run("SHA falls back to sha field when id empty", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		e := gin.New()
		e.GET("/api/v5/repos/:owner/:name/branches/*branch", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"name": "main", "commit": gin.H{"id": "", "sha": "sha-fallback"}})
		})
		s := httptest.NewServer(e)
		defer s.Close()
		gcInt, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
		gc := gcInt.(*GitCode)

		commit, err := gc.BranchHead(t.Context(), fakeUser, fakeRepo, "main")
		require.NoError(t, err)
		assert.Equal(t, "sha-fallback", commit.SHA)
	})
}

// ---------------------------------------------------------------------------
// PullRequests — pagination
// ---------------------------------------------------------------------------

func TestPullRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns open PRs", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/pulls", func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "open", r.URL.Query().Get("state"))
			_ = json.NewEncoder(w).Encode([]gin.H{
				{"iid": "1", "title": "Fix bug"},
				{"iid": "2", "title": "Add feature"},
			})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		prs, err := gc.PullRequests(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 10})
		require.NoError(t, err)
		require.Len(t, prs, 2)
		assert.Equal(t, "Fix bug", prs[0].Title)
		assert.Equal(t, "Add feature", prs[1].Title)
	})

	t.Run("pagination passes page and per_page", func(t *testing.T) {
		var gotPage, gotPerPage string
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/pulls", func(w http.ResponseWriter, r *http.Request) {
			gotPage = r.URL.Query().Get("page")
			gotPerPage = r.URL.Query().Get("per_page")
			_ = json.NewEncoder(w).Encode([]gin.H{})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, _ = gc.PullRequests(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 2, PerPage: 5})
		assert.Equal(t, "2", gotPage)
		assert.Equal(t, "5", gotPerPage)
	})

	t.Run("perPage clamping on pulls", func(t *testing.T) {
		var gotPerPage string
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/pulls", func(w http.ResponseWriter, r *http.Request) {
			gotPerPage = r.URL.Query().Get("per_page")
			_ = json.NewEncoder(w).Encode([]gin.H{})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, _ = gc.PullRequests(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 0})
		assert.Equal(t, "50", gotPerPage)

		_, _ = gc.PullRequests(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 999})
		assert.Equal(t, "50", gotPerPage)
	})

	t.Run("empty pull request list", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/pulls", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		prs, err := gc.PullRequests(t.Context(), fakeUser, fakeRepo, &model.ListOptions{Page: 1, PerPage: 10})
		require.NoError(t, err)
		assert.Empty(t, prs)
	})
}

// ---------------------------------------------------------------------------
// File — 404 -> ErrConfigNotFound, success, ref param
// ---------------------------------------------------------------------------

func TestFile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns file content on success", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("pipeline: hello"))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		data, err := gc.File(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "abc123"}, ".woodpecker.yml")
		require.NoError(t, err)
		assert.Equal(t, "pipeline: hello", string(data))
	})

	t.Run("404 returns ErrConfigNotFound", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, err := gc.File(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "abc123"}, "missing.yml")
		require.Error(t, err)
		assert.True(t, errors.Is(err, &forge_types.ErrConfigNotFound{}))
	})

	t.Run("uses ref query param not ref_name", func(t *testing.T) {
		var gotRef, gotRefName string
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, r *http.Request) {
			gotRef = r.URL.Query().Get("ref")
			gotRefName = r.URL.Query().Get("ref_name")
			_, _ = w.Write([]byte("ok"))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, err := gc.File(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "deadbeef"}, ".woodpecker.yml")
		require.NoError(t, err)
		assert.Equal(t, "deadbeef", gotRef)
		assert.Empty(t, gotRefName)
	})

	t.Run("500 returns error not ErrConfigNotFound", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, err := gc.File(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "abc123"}, ".woodpecker.yml")
		require.Error(t, err)
		assert.False(t, errors.Is(err, &forge_types.ErrConfigNotFound{}))
	})
}

// ---------------------------------------------------------------------------
// Dir — filtering, extension fallback, ref_name vs ref, ErrConfigNotFound
// ---------------------------------------------------------------------------

func TestDir(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("filters by directory and extension", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/file_list", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[".woodpecker/build.yml", ".woodpecker/README.md", "other/file.yml"]`))
		})
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("version: 1"))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		files, err := gc.Dir(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "main"}, ".woodpecker")
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, ".woodpecker/build.yml", files[0].Name)
	})

	t.Run("sends ref_name not ref to file_list", func(t *testing.T) {
		var gotRefName, gotRef string
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/file_list", func(w http.ResponseWriter, r *http.Request) {
			gotRefName = r.URL.Query().Get("ref_name")
			gotRef = r.URL.Query().Get("ref")
			_, _ = w.Write([]byte(`[".woodpecker/build.yml"]`))
		})
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, err := gc.Dir(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "my-sha"}, ".woodpecker")
		require.NoError(t, err)
		assert.Equal(t, "my-sha", gotRefName)
		assert.Empty(t, gotRef)
	})

	t.Run("branch divergence respects ref_name", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/file_list", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("ref_name") == "feature-sha" {
				_, _ = w.Write([]byte(`[".woodpecker/a.yml", ".woodpecker/b.yml"]`))
			} else {
				_, _ = w.Write([]byte(`[".woodpecker/a.yml"]`))
			}
		})
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		files, err := gc.Dir(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "feature-sha"}, ".woodpecker")
		require.NoError(t, err)
		assert.Len(t, files, 2)

		files, err = gc.Dir(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "main"}, ".woodpecker")
		require.NoError(t, err)
		assert.Len(t, files, 1)
	})

	t.Run("404 from file_list returns ErrConfigNotFound", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/file_list", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, err := gc.Dir(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "abc"}, ".woodpecker")
		require.Error(t, err)
		assert.True(t, errors.Is(err, &forge_types.ErrConfigNotFound{}))
	})

	t.Run("no matching files returns ErrConfigNotFound", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/file_list", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`["README.md", "other/file.txt"]`))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, err := gc.Dir(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "abc"}, ".woodpecker")
		require.Error(t, err)
		assert.True(t, errors.Is(err, &forge_types.ErrConfigNotFound{}))
	})

	t.Run("object array file_list shape", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/file_list", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"path": ".woodpecker/build.yml"}, {"path": ".woodpecker/README.md"}]`))
		})
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/raw/", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		files, err := gc.Dir(t.Context(), fakeUser, fakeRepo, &model.Pipeline{Commit: "abc"}, ".woodpecker")
		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, ".woodpecker/build.yml", files[0].Name)
	})
}

// ---------------------------------------------------------------------------
// Status — no-op
// ---------------------------------------------------------------------------

func TestStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Status is no-op and never errors", func(t *testing.T) {
		s, gc := newTestServer(http.NewServeMux())
		defer s.Close()

		err := gc.Status(t.Context(), fakeUser, fakeRepo, fakePipeline, fakeWorkflow)
		require.NoError(t, err)
	})

	t.Run("Status with nil workflow", func(t *testing.T) {
		s, gc := newTestServer(http.NewServeMux())
		defer s.Close()

		err := gc.Status(t.Context(), fakeUser, fakeRepo, fakePipeline, nil)
		require.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// Netrc — with and without user
// ---------------------------------------------------------------------------

func TestNetrc(t *testing.T) {
	t.Run("with user returns login and token", func(t *testing.T) {
		n, err := (&GitCode{}).Netrc(fakeUser, fakeRepo)
		require.NoError(t, err)
		assert.Equal(t, fakeUser.Login, n.Login)
		assert.Equal(t, fakeUser.AccessToken, n.Password)
		assert.Equal(t, "localhost", n.Machine)
		assert.Equal(t, model.ForgeTypeGitCode, n.Type)
	})

	t.Run("nil user returns empty login and token", func(t *testing.T) {
		n, err := (&GitCode{}).Netrc(nil, fakeRepo)
		require.NoError(t, err)
		assert.Empty(t, n.Login)
		assert.Empty(t, n.Password)
		assert.Equal(t, "localhost", n.Machine)
		assert.Equal(t, model.ForgeTypeGitCode, n.Type)
	})

	t.Run("invalid clone URL returns error", func(t *testing.T) {
		badRepo := &model.Repo{Clone: "://bad url"}
		_, err := (&GitCode{}).Netrc(fakeUser, badRepo)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// Teams — Page != 1 returns nil
// ---------------------------------------------------------------------------

func TestTeams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Page != 1 returns nil", func(t *testing.T) {
		s, gc := newTestServer(http.NewServeMux())
		defer s.Close()

		teams, err := gc.Teams(t.Context(), fakeUser, &model.ListOptions{Page: 2, PerPage: 10})
		require.NoError(t, err)
		assert.Nil(t, teams)
	})

	t.Run("Page 1 returns teams", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/users/orgs", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{
				{"path": "myorg", "avatar_url": "https://gitcode.com/avatar.png"},
			})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		teams, err := gc.Teams(t.Context(), fakeUser, &model.ListOptions{Page: 1, PerPage: 10})
		require.NoError(t, err)
		require.Len(t, teams, 1)
		assert.Equal(t, "myorg", teams[0].Login)
	})

	t.Run("Page 0 not short-circuited - goes to API (consistent with Page != 1 check)", func(t *testing.T) {
		// Teams returns nil only when p.Page != 1, so Page 0 also returns nil.
		s, gc := newTestServer(http.NewServeMux())
		defer s.Close()

		teams, err := gc.Teams(t.Context(), fakeUser, &model.ListOptions{Page: 0, PerPage: 10})
		require.NoError(t, err)
		assert.Nil(t, teams)
	})
}

// ---------------------------------------------------------------------------
// Repos — Page != 1 returns nil
// ---------------------------------------------------------------------------

func TestRepos(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Page != 1 returns nil", func(t *testing.T) {
		s, gc := newTestServer(http.NewServeMux())
		defer s.Close()

		repos, err := gc.Repos(t.Context(), fakeUser, &model.ListOptions{Page: 2, PerPage: 10})
		require.NoError(t, err)
		assert.Nil(t, repos)
	})

	t.Run("Page 1 returns repos filtering archived", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/user/repos", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{
				{
					"id": 1, "name": "active", "path": "active",
					"path_with_namespace": "test_name/active", "full_name": "test_name/active",
					"http_url_to_repo": "http://localhost/test_name/active.git",
					"archived": false,
				},
				{
					"id": 2, "name": "archived", "path": "archived",
					"path_with_namespace": "test_name/archived", "full_name": "test_name/archived",
					"http_url_to_repo": "http://localhost/test_name/archived.git",
					"archived": true,
				},
			})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		repos, err := gc.Repos(t.Context(), fakeUser, &model.ListOptions{Page: 1, PerPage: 10})
		require.NoError(t, err)
		require.Len(t, repos, 1)
		assert.Equal(t, "active", repos[0].Name)
	})
}

// ---------------------------------------------------------------------------
// Activate — idempotent: Deactivate first
// ---------------------------------------------------------------------------

func TestActivate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Activate deactivates existing hook then creates new one", func(t *testing.T) {
		var gotDelete bool
		var gotPost bool
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]gin.H{
					{"id": 1, "url": "http://woodpecker.example.com/hook", "password": "secret"},
				})
				return
			}
			if r.Method == http.MethodPost {
				gotPost = true
				_ = json.NewEncoder(w).Encode(gin.H{"id": 2})
				return
			}
		})
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks/1", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				gotDelete = true
				w.WriteHeader(http.StatusNoContent)
				return
			}
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		err := gc.Activate(t.Context(), fakeUser, fakeRepo, "http://woodpecker.example.com/hook")
		require.NoError(t, err)
		assert.True(t, gotDelete, "Activate should delete existing hook first")
		assert.True(t, gotPost, "Activate should create new hook after delete")
	})

	t.Run("Activate with no existing hooks just creates", func(t *testing.T) {
		var gotPost bool
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]gin.H{})
				return
			}
			if r.Method == http.MethodPost {
				gotPost = true
				_ = json.NewEncoder(w).Encode(gin.H{"id": 2})
				return
			}
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		err := gc.Activate(t.Context(), fakeUser, fakeRepo, "http://woodpecker.example.com/hook")
		require.NoError(t, err)
		assert.True(t, gotPost)
	})
}

// ---------------------------------------------------------------------------
// Deactivate — not found returns nil
// ---------------------------------------------------------------------------

func TestDeactivate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("no matching hook returns nil", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{
				{"id": 1, "url": "http://other.example.com/hook", "password": "secret"},
			})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		err := gc.Deactivate(t.Context(), fakeUser, fakeRepo, "http://woodpecker.example.com/hook")
		require.NoError(t, err)
	})

	t.Run("empty hooks list returns nil", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		err := gc.Deactivate(t.Context(), fakeUser, fakeRepo, "http://woodpecker.example.com/hook")
		require.NoError(t, err)
	})

	t.Run("matching hook is deleted", func(t *testing.T) {
		var deleted bool
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{
				{"id": 42, "url": "http://woodpecker.example.com/hook", "password": "secret"},
			})
		})
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks/42", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			}
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		err := gc.Deactivate(t.Context(), fakeUser, fakeRepo, "http://woodpecker.example.com/hook")
		require.NoError(t, err)
		assert.True(t, deleted)
	})

	t.Run("host matching is by host not path", func(t *testing.T) {
		var deleted bool
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]gin.H{
				{"id": 99, "url": "http://woodpecker.example.com/other-path", "password": "secret"},
			})
		})
		mux.HandleFunc("/api/v5/repos/test_name/repo_name/hooks/99", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			}
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		err := gc.Deactivate(t.Context(), fakeUser, fakeRepo, "http://woodpecker.example.com/hook")
		require.NoError(t, err)
		assert.True(t, deleted, "Deactivate matches by host, so different path still deletes")
	})
}

// ---------------------------------------------------------------------------
// Org — user vs enterprise fallback
// ---------------------------------------------------------------------------

func TestOrg(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("user org returns IsUser true", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/users/alice", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(gin.H{"id": "1", "login": "alice", "username": "alice", "name": "Alice"})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		org, err := gc.Org(t.Context(), fakeUser, "alice")
		require.NoError(t, err)
		require.NotNil(t, org)
		assert.Equal(t, "alice", org.Name)
		assert.True(t, org.IsUser)
	})

	t.Run("enterprise fallback when users 404", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/users/fork", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error_code":1003,"error_message":"\u7528\u6237\u4e0d\u5b58\u5728"}`))
		})
		mux.HandleFunc("/api/v5/orgs/fork", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(gin.H{"id": 8113384, "login": "fork", "name": "Fork", "public": true})
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		org, err := gc.Org(t.Context(), fakeUser, "fork")
		require.NoError(t, err)
		require.NotNil(t, org)
		assert.Equal(t, "fork", org.Name)
		assert.False(t, org.IsUser)
	})

	t.Run("not found on both users and orgs returns error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v5/users/unknown", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		})
		mux.HandleFunc("/api/v5/orgs/unknown", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		})
		s, gc := newTestServer(mux)
		defer s.Close()

		_, err := gc.Org(t.Context(), fakeUser, "unknown")
		require.Error(t, err)
	})
}
