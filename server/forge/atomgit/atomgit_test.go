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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go.woodpecker-ci.org/woodpecker/v3/server"
	"go.woodpecker-ci.org/woodpecker/v3/server/forge/atomgit/fixtures"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
	"go.woodpecker-ci.org/woodpecker/v3/server/store"
	store_mocks "go.woodpecker-ci.org/woodpecker/v3/server/store/mocks"
)

func TestNew(t *testing.T) {
	forge, _ := New(1, Opts{
		URL:        "http://localhost:8080",
		SkipVerify: true,
	})

	f, _ := forge.(*AtomGit)
	assert.Equal(t, "http://localhost:8080", f.url)
	assert.True(t, f.skipVerify)
	assert.Equal(t, "atomgit", f.Name())
}

// Test_user_unmarshal verifies AtomGit's /api/v5/user payload decodes
// correctly. AtomGit returns the id as an opaque hex string (e.g.
// "6638af02bbeee41d0fe74c35"), never a number, and the login field is
// "login" rather than "username". This guards the login regression where the
// id could not be unmarshaled.
func Test_user_unmarshal(t *testing.T) {
	var u user
	// AtomGit returns id as a hex string and the login field as "login".
	err := json.Unmarshal([]byte(`{"id":"6638af02bbeee41d0fe74c35","login":"someuser","name":"Some User","email":"a@b.com"}`), &u)
	assert.NoError(t, err)
	assert.Equal(t, "6638af02bbeee41d0fe74c35", u.ID.String())
	assert.Equal(t, "someuser", u.Username)

	w := toUser(&u)
	assert.Equal(t, model.ForgeRemoteID("6638af02bbeee41d0fe74c35"), w.ForgeRemoteID)
	assert.Equal(t, "someuser", w.Login)
}

func Test_atomgit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := httptest.NewServer(fixtures.Handler())
	defer s.Close()
	c, _ := New(1, Opts{
		URL:        s.URL,
		SkipVerify: true,
	})

	mockStore := store_mocks.NewMockStore(t)
	ctx := store.InjectToContext(t.Context(), mockStore)

	t.Run("repository details", func(t *testing.T) {
		repo, err := c.Repo(ctx, fakeUser, fakeRepo.ForgeRemoteID, fakeRepo.Owner, fakeRepo.Name)
		assert.NoError(t, err)
		assert.Equal(t, fakeRepo.Owner, repo.Owner)
		assert.Equal(t, fakeRepo.Name, repo.Name)
		assert.Equal(t, fakeRepo.Owner+"/"+fakeRepo.Name, repo.FullName)
		assert.Equal(t, "http://localhost/test_name/repo_name.git", repo.Clone)
		assert.Equal(t, "http://localhost/test_name/repo_name", repo.ForgeURL)
	})
	t.Run("repo not found", func(t *testing.T) {
		_, err := c.Repo(ctx, fakeUser, "0", fakeRepoNotFound.Owner, fakeRepoNotFound.Name)
		assert.Error(t, err)
	})

	t.Run("repository list", func(t *testing.T) {
		repos, err := c.Repos(ctx, fakeUser, &model.ListOptions{Page: 1, PerPage: 10})
		assert.NoError(t, err)
		assert.Equal(t, fakeRepo.ForgeRemoteID, repos[0].ForgeRemoteID)
		assert.Equal(t, fakeRepo.Owner, repos[0].Owner)
		assert.Equal(t, fakeRepo.Name, repos[0].Name)
	})

	t.Run("register repository", func(t *testing.T) {
		err := c.Activate(ctx, fakeUser, fakeRepo, "http://localhost")
		assert.NoError(t, err)
	})

	t.Run("remove hooks", func(t *testing.T) {
		err := c.Deactivate(ctx, fakeUser, fakeRepo, "http://localhost")
		assert.NoError(t, err)
	})

	t.Run("repository file", func(t *testing.T) {
		raw, err := c.File(ctx, fakeUser, fakeRepo, fakePipeline, ".woodpecker.yml")
		assert.NoError(t, err)
		assert.Equal(t, "{ platform: linux/amd64 }", string(raw))
	})

	t.Run("pipeline status is no-op", func(t *testing.T) {
		err := c.Status(ctx, fakeUser, fakeRepo, fakePipeline, fakeWorkflow)
		assert.NoError(t, err)
	})

	t.Run("PR hook", func(t *testing.T) {
		buf := bytes.NewBufferString(fixtures.HookMergeRequest)
		req, _ := http.NewRequest(http.MethodPost, "/hook", buf)
		req.Header = http.Header{}
		req.Header.Set(hookEvent, hookMergeRequest)
		mockStore.On("GetRepoNameFallback", mock.Anything, mock.Anything, mock.Anything).Return(fakeRepo, nil)
		mockStore.On("GetUser", mock.Anything).Return(fakeUser, nil)
		r, b, err := c.Hook(ctx, req)
		assert.NotNil(t, r)
		assert.NotNil(t, b)
		assert.NoError(t, err)
		assert.Equal(t, model.EventPull, b.Event)
	})

	t.Run("branch head synthesizes commit url", func(t *testing.T) {
		commit, err := c.BranchHead(ctx, fakeUser, fakeRepo, "develop")
		assert.NoError(t, err)
		assert.Equal(t, "8240e6b568", commit.SHA)
		// The branch endpoint carries no commit web URL, so BranchHead must
		// synthesize one from the repo forge URL using the
		// {repo}/commits/detail/{sha} scheme, otherwise a manual pipeline's
		// forge_url is empty and the commit link on the pipeline page reloads
		// the current page.
		assert.Equal(t, "http://localhost/test_name/repo_name/commits/detail/8240e6b568", commit.ForgeURL)
	})

	t.Run("netrc", func(t *testing.T) {
		netrc, err := c.Netrc(fakeUser, fakeRepo)
		assert.NoError(t, err)
		assert.Equal(t, "localhost", netrc.Machine)
		assert.Equal(t, fakeUser.Login, netrc.Login)
		assert.Equal(t, model.ForgeTypeAtomGit, netrc.Type)
	})
}

// Test_atomgit_repoByIDScan_noNamespace reproduces the bug where the
// /user/repos summary omits the namespace object. Repo() must still upgrade
// the matched summary to the full repository (via GET /repos/:owner/:name) so
// that forge_url (html_url) and pr_enabled (has_pull_requests) are populated.
func Test_atomgit_repoByIDScan_noNamespace(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mux := http.NewServeMux()
	// Force the primary GET /repositories/:id path to 404 so Repo() falls
	// back to getRepoByIDScan.
	mux.HandleFunc("/api/v5/repositories/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found","code":404}`))
	})
	// /user/repos summary WITHOUT a namespace object, but with the fields
	// needed to derive owner/name (path_with_namespace / full_name).
	mux.HandleFunc("/api/v5/user/repos", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{
			"id": 5,
			"name": "repo_name",
			"path_with_namespace": "test_name/repo_name",
			"full_name": "test_name/repo_name",
			"html_url": "http://localhost/test_name/repo_name",
			"has_pull_requests": true
		}]`))
	})
	// Full repo fetch returns html_url + has_pull_requests.
	mux.HandleFunc("/api/v5/repos/test_name/repo_name", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"id": 5,
			"name": "repo_name",
			"path_with_namespace": "test_name/repo_name",
			"full_name": "test_name/repo_name",
			"html_url": "http://localhost/test_name/repo_name",
			"http_url_to_repo": "http://localhost/test_name/repo_name.git",
			"ssh_url_to_repo": "git@localhost:test_name/repo_name.git",
			"default_branch": "main",
			"public": true,
			"has_pull_requests": true
		}`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})

	mockStore := store_mocks.NewMockStore(t)
	ctx := store.InjectToContext(t.Context(), mockStore)

	// Call Repo() with only the remoteID (no owner/name) so the by-id scan
	// path is exercised.
	repo, err := c.Repo(ctx, fakeUser, "5", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "http://localhost/test_name/repo_name", repo.ForgeURL)
	assert.True(t, repo.PREnabled)
}

// Test_atomgit_repo_byIDIncomplete verifies that when GET /repositories/:id
// succeeds but returns an incomplete payload (no html_url), Repo() does NOT
// trust it and instead upgrades to the full repository via GET /repos/:owner/:name
// (or the /user/repos scan), so forge_url is still populated. This is the exact
// bug that produced an empty forge_url -> "<button>" on the repo page.
func Test_atomgit_repo_byIDIncomplete(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mux := http.NewServeMux()
	// /repositories/:id returns 200 but WITHOUT html_url / has_pull_requests.
	mux.HandleFunc("/api/v5/repositories/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": 5, "name": "repo_name", "full_name": "test_name/repo_name"}`))
	})
	// /user/repos scan (used by getRepoByIDScan fallback) returns the summary.
	mux.HandleFunc("/api/v5/user/repos", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id": 5, "path_with_namespace": "test_name/repo_name", "full_name": "test_name/repo_name"}]`))
	})
	// Full repo fetch carries html_url + has_pull_requests.
	mux.HandleFunc("/api/v5/repos/test_name/repo_name", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"id": 5,
			"name": "repo_name",
			"path_with_namespace": "test_name/repo_name",
			"full_name": "test_name/repo_name",
			"html_url": "http://localhost/test_name/repo_name",
			"http_url_to_repo": "http://localhost/test_name/repo_name.git",
			"ssh_url_to_repo": "git@localhost:test_name/repo_name.git",
			"default_branch": "main",
			"public": true,
			"has_pull_requests": true
		}`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})

	mockStore := store_mocks.NewMockStore(t)
	ctx := store.InjectToContext(t.Context(), mockStore)

	// Call with a valid remoteID AND owner/name; the by-id path is incomplete
	// and must be upgraded to the full repo.
	repo, err := c.Repo(ctx, fakeUser, "5", "test_name", "repo_name")
	assert.NoError(t, err)
	assert.Equal(t, "http://localhost/test_name/repo_name", repo.ForgeURL)
	assert.True(t, repo.PREnabled)
}

// Test_atomgit_BranchHead_preservesAPIURL verifies that when the branch
// endpoint DOES return an absolute commit web URL, BranchHead uses it as-is
// rather than synthesizing one.
func Test_atomgit_BranchHead_preservesAPIURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/test_name/repo_name/branches/develop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"name": "develop",
			"commit": {
				"id": "8240e6b568",
				"url": "https://atomgit.com/test_name/repo_name/commits/detail/8240e6b568"
			}
		}`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})

	commit, err := c.BranchHead(t.Context(), fakeUser, fakeRepo, "develop")
	assert.NoError(t, err)
	assert.Equal(t, "8240e6b568", commit.SHA)
	assert.Equal(t, "https://atomgit.com/test_name/repo_name/commits/detail/8240e6b568", commit.ForgeURL)
}

var (
	fakeUser = &model.User{
		Login:       "someuser",
		AccessToken: "cfcd2084",
	}

	fakeRepo = &model.Repo{
		Clone:         "http://localhost/test_name/repo_name.git",
		ForgeRemoteID: "5",
		Owner:         "test_name",
		Name:          "repo_name",
		FullName:      "test_name/repo_name",
		ForgeURL:      "http://localhost/test_name/repo_name",
		Hash:          "secret",
	}

	fakeRepoNotFound = &model.Repo{
		Owner:    "test_name",
		Name:     "repo_not_found",
		FullName: "test_name/repo_not_found",
	}

	fakePipeline = &model.Pipeline{
		Commit: "9ecad50",
	}

	fakeWorkflow = &model.Workflow{
		Name:  "test",
		State: model.StatusSuccess,
	}
)

// Test_expandAvatar_CDN_to_proxy verifies that CDN avatar URLs are converted
// to the Woodpecker proxy endpoint, while normal URLs remain unchanged.
func Test_expandAvatar_CDN_to_proxy(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		rawURL  string
		want    string
	}{
		{
			name:    "empty URL stays empty",
			repoURL: "https://gitcode.com/user/repo",
			rawURL:  "",
			want:    "",
		},
		{
			name:    "normal absolute URL stays unchanged",
			repoURL: "https://atomgit.com/someuser",
			rawURL:  "https://atomgit.com/avatar.png",
			want:    "https://atomgit.com/avatar.png",
		},
		{
			name:    "relative URL resolved against repo URL",
			repoURL: "https://atomgit.com/someuser",
			rawURL:  "/uploads/avatar.png",
			want:    "https://atomgit.com/uploads/avatar.png",
		},
		{
			name:    "GitCode CDN URL converted to proxy",
			repoURL: "https://gitcode.com/jetsung",
			rawURL:  "https://cdn-img.gitcode.com/bf/ee/b4f489e3933733e085b0f6ad0073345142d939d9e53c67d7e9445c66b71ad0a0.JPG?time=1705574695777",
			want:    "/api/avatar-proxy?referer=https%3A%2F%2Fgitcode.com&url=https%3A%2F%2Fcdn-img.gitcode.com%2Fbf%2Fee%2Fb4f489e3933733e085b0f6ad0073345142d939d9e53c67d7e9445c66b71ad0a0.JPG%3Ftime%3D1705574695777",
		},
		{
			name:    "AtomGit CDN URL converted to proxy",
			repoURL: "https://atomgit.com/someuser",
			rawURL:  "https://cdn-img.atomgit.com/some/path.jpg",
			want:    "/api/avatar-proxy?referer=https%3A%2F%2Fatomgit.com&url=https%3A%2F%2Fcdn-img.atomgit.com%2Fsome%2Fpath.jpg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandAvatar(tt.repoURL, tt.rawURL)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test_sshURLFromClone_derivesFromHTTPHost verifies the SSH clone URL is built
// from the HTTP clone URL's host.
func Test_sshURLFromClone_derivesFromHTTPHost(t *testing.T) {
	cases := []struct {
		name  string
		clone string
		owner string
		repo  string
		want  string
	}{
		{"atomgit https", "https://atomgit.com/jetsung/testci.git", "jetsung", "testci", "git@atomgit.com:jetsung/testci.git"},
		{"atomgit http", "http://atomgit.com/jetsung/testci.git", "jetsung", "testci", "git@atomgit.com:jetsung/testci.git"},
		{"same as http host", "https://atomgit.com/jetsung/testci.git", "jetsung", "testci", "git@atomgit.com:jetsung/testci.git"},
		{"empty clone", "", "jetsung", "testci", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sshURLFromClone(c.clone, c.owner, c.repo)
			assert.Equal(t, c.want, got)
		})
	}
}

func Test_atomgit_Dir_filtering(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotRefName string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/jetsung/testci/file_list", func(w http.ResponseWriter, r *http.Request) {
		gotRefName = r.URL.Query().Get("ref_name")
		_, _ = w.Write([]byte(`[
			".woodpecker/build.yml",
			".woodpecker/README.md",
			"dotnet/dotnet.csproj",
			"dotnet/Program.cs",
			"main.c",
			"main.cpp"
		]`))
	})
	mux.HandleFunc("/api/v5/repos/jetsung/testci/raw/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`version: 1`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
	repo := &model.Repo{Owner: "jetsung", Name: "testci", FullName: "jetsung/testci"}
	pipeline := &model.Pipeline{Commit: "master"}

	files, err := c.Dir(t.Context(), fakeUser, repo, pipeline, ".woodpecker")
	assert.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, ".woodpecker/build.yml", files[0].Name)
	assert.Equal(t, "version: 1", string(files[0].Data))
	// /file_list identifies the revision via ref_name, not ref.
	assert.Equal(t, "master", gotRefName)

	_, err = c.Dir(t.Context(), fakeUser, repo, pipeline, "dotnet")
	assert.Error(t, err)
}

// Test_atomgit_Dir_objectArray verifies Dir() parses the object-array
// file_list shape ([{"path":...}]) and only fetches the matching config.
func Test_atomgit_Dir_objectArray(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotRefName string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/jetsung/testci/file_list", func(w http.ResponseWriter, r *http.Request) {
		gotRefName = r.URL.Query().Get("ref_name")
		_, _ = w.Write([]byte(`[
			{"path": ".woodpecker/build.yml", "type": "file", "name": "build.yml"},
			{"path": ".woodpecker/README.md", "type": "file", "name": "README.md"}
		]`))
	})
	mux.HandleFunc("/api/v5/repos/jetsung/testci/raw/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`version: 1`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
	repo := &model.Repo{Owner: "jetsung", Name: "testci", FullName: "jetsung/testci"}
	pipeline := &model.Pipeline{Commit: "master"}

	files, err := c.Dir(t.Context(), fakeUser, repo, pipeline, ".woodpecker")
	assert.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, ".woodpecker/build.yml", files[0].Name)
	assert.Equal(t, "master", gotRefName)
}

// Test_atomgit_Dir_configExtensions verifies the directory listing honours the
// operator-configured extension set, and falls back to .yaml/.yml when unset.
// Filtering on an empty configured set would discard everything and leave the
// repository with no pipelines at all.
func Test_atomgit_Dir_configExtensions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/jetsung/testci/file_list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			".woodpecker/build.yml",
			".woodpecker/deploy.star",
			".woodpecker/README.md"
		]`))
	})
	mux.HandleFunc("/api/v5/repos/jetsung/testci/raw/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`version: 1`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
	repo := &model.Repo{Owner: "jetsung", Name: "testci", FullName: "jetsung/testci"}
	pipeline := &model.Pipeline{Commit: "master"}

	original := server.Config.Pipeline.ConfigExtensions
	defer func() { server.Config.Pipeline.ConfigExtensions = original }()

	// Configured set includes a non-default extension: it must survive.
	server.Config.Pipeline.ConfigExtensions = []string{".yml", ".star"}
	files, err := c.Dir(t.Context(), fakeUser, repo, pipeline, ".woodpecker")
	assert.NoError(t, err)
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	assert.ElementsMatch(t, []string{".woodpecker/build.yml", ".woodpecker/deploy.star"}, names)

	// Unset: fall back to .yaml/.yml rather than filtering everything out.
	server.Config.Pipeline.ConfigExtensions = nil
	files, err = c.Dir(t.Context(), fakeUser, repo, pipeline, ".woodpecker")
	assert.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, ".woodpecker/build.yml", files[0].Name)
}

// Test_atomgit_Dir_branchDivergence is the regression test for the bug where
// Dir() sent ?ref= to /file_list. That endpoint only accepts ref_name, so
// AtomGit silently ignored the unknown parameter and returned the default
// branch tree: pushing to a branch with extra pipeline configs only ever
// triggered the workflows that also existed on the default branch.
func Test_atomgit_Dir_branchDivergence(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const developSHA = "b1946ac92492d2347c6235b4d2611184"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/jetsung/testci/file_list", func(w http.ResponseWriter, r *http.Request) {
		// The default branch only carries go.yml; develop adds c.yml and cpp.yml.
		if r.URL.Query().Get("ref_name") == developSHA {
			_, _ = w.Write([]byte(`[
				".woodpecker/go.yml",
				".woodpecker/c.yml",
				".woodpecker/cpp.yml"
			]`))
			return
		}
		_, _ = w.Write([]byte(`[".woodpecker/go.yml"]`))
	})
	mux.HandleFunc("/api/v5/repos/jetsung/testci/raw/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`version: 1`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
	repo := &model.Repo{Owner: "jetsung", Name: "testci", FullName: "jetsung/testci"}

	files, err := c.Dir(t.Context(), fakeUser, repo, &model.Pipeline{Commit: developSHA}, ".woodpecker")
	assert.NoError(t, err)
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	assert.ElementsMatch(t, []string{".woodpecker/go.yml", ".woodpecker/c.yml", ".woodpecker/cpp.yml"}, names)

	// A push to the default branch must still see only its own config.
	files, err = c.Dir(t.Context(), fakeUser, repo, &model.Pipeline{Commit: "main"}, ".woodpecker")
	assert.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, ".woodpecker/go.yml", files[0].Name)
}

// Test_atomgit_File_usesRefParam pins File() to the ref query parameter.
// /raw/{path} takes ref while /file_list takes ref_name, so the Dir() fix must
// not be applied to both endpoints.
func Test_atomgit_File_usesRefParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotRef, gotRefName string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/jetsung/testci/raw/", func(w http.ResponseWriter, r *http.Request) {
		gotRef = r.URL.Query().Get("ref")
		gotRefName = r.URL.Query().Get("ref_name")
		_, _ = w.Write([]byte(`version: 1`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
	repo := &model.Repo{Owner: "jetsung", Name: "testci", FullName: "jetsung/testci"}
	pipeline := &model.Pipeline{Commit: "deadbeef"}

	_, err := c.File(t.Context(), fakeUser, repo, pipeline, ".woodpecker/go.yml")
	assert.NoError(t, err)
	assert.Equal(t, "deadbeef", gotRef)
	assert.Empty(t, gotRefName)
}

// Test_atomgit_File_subfolderPath verifies that File() escapes each path
// segment while preserving the forward slashes, so a subfolder raw URL like
// /raw/dotnet/dotnet.csproj reaches the backend router (not %2F-encoded).
func Test_atomgit_File_subfolderPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotRawPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v5/repos/jetsung/testci/raw/", func(w http.ResponseWriter, r *http.Request) {
		gotRawPath = strings.TrimPrefix(r.URL.Path, "/api/v5/repos/jetsung/testci/raw/")
		_, _ = w.Write([]byte(`hello`))
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	c, _ := New(1, Opts{URL: s.URL, SkipVerify: true})
	repo := &model.Repo{Owner: "jetsung", Name: "testci", FullName: "jetsung/testci"}
	pipeline := &model.Pipeline{Commit: "master"}

	_, err := c.File(t.Context(), fakeUser, repo, pipeline, "dotnet/dotnet.csproj")
	assert.NoError(t, err)
	// Forward slashes preserved; segments individually escaped.
	assert.Equal(t, "dotnet/dotnet.csproj", gotRawPath)
	assert.NotContains(t, gotRawPath, "%2F")
}
