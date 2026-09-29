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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

// Test_toRepo covers full_name / path_with_namespace / namespace fallback,
// PageURL priority (html_url > web_url > homepage > url), clone fallback chain
// (http_url_to_repo > git_http_url > web_url), and sshURL derivation.
func Test_toRepo(t *testing.T) {
	t.Run("full_name takes precedence over others", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:                id("1"),
			FullName:          "owner/repo",
			PathWithNamespace: "other/ignored",
			Name:              "ignored",
			Namespace:         &namespaceRef{namespace: &namespace{Path: "other2"}},
			HTMLURL:           "https://gitcode.com/owner/repo",
			GitURL:            "https://gitcode.com/owner/repo.git",
		})
		assert.Equal(t, "owner", repo.Owner)
		assert.Equal(t, "repo", repo.Name)
		assert.Equal(t, "owner/repo", repo.FullName)
	})

	t.Run("path_with_namespace fallback when full_name empty", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:                id("1"),
			PathWithNamespace: "fallback/repo",
			HTMLURL:           "https://gitcode.com/fallback/repo",
		})
		assert.Equal(t, "fallback", repo.Owner)
		assert.Equal(t, "repo", repo.Name)
		assert.Equal(t, "fallback/repo", repo.FullName)
	})

	t.Run("namespace fallback when full_name and path_with_namespace empty", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:        id("1"),
			Name:      "myrepo",
			Namespace: &namespaceRef{namespace: &namespace{Path: "nsowner", Name: "nsowner"}},
			HTMLURL:   "https://gitcode.com/nsowner/myrepo",
		})
		assert.Equal(t, "nsowner", repo.Owner)
		assert.Equal(t, "myrepo", repo.Name)
		assert.Equal(t, "nsowner/myrepo", repo.FullName)
	})

	t.Run("pageURL clone fallback chain via extractOwnerNameFromURL", func(t *testing.T) {
		tests := []struct {
			name string
			repo *repository
			wantOwner string
			wantName  string
		}{
			{
				name: "web_url only",
				repo: &repository{ID: id("1"), WebURL: "https://gitcode.com/webowner/webrepo"},
				wantOwner: "webowner", wantName: "webrepo",
			},
			{
				name: "homepage fallback",
				repo: &repository{ID: id("1"), Homepage: "https://gitcode.com/homeowner/homerepo"},
				wantOwner: "homeowner", wantName: "homerepo",
			},
			{
				name: "git_http_url fallback",
				repo: &repository{ID: id("1"), GitHTTPURL: "https://gitcode.com/httpowner/httprepo.git"},
				wantOwner: "httpowner", wantName: "httprepo",
			},
			{
				name: "http_url_to_repo primary",
				repo: &repository{ID: id("1"), GitURL: "https://gitcode.com/cloneowner/clonerepo.git"},
				wantOwner: "cloneowner", wantName: "clonerepo",
			},
			{
				name: "ssh clone fallback git@host:owner/repo",
				repo: &repository{ID: id("1"), GitSSHURL: "git@gitcode.com:sshowner/sshrepo.git"},
				wantOwner: "sshowner", wantName: "sshrepo",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				r := toRepo(tc.repo)
				assert.Equal(t, tc.wantOwner, r.Owner)
				assert.Equal(t, tc.wantName, r.Name)
			})
		}
	})

	t.Run("pageURL priority html_url over web_url", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:       id("1"),
			FullName: "a/b",
			HTMLURL:  "https://gitcode.com/a/b",
			WebURL:   "https://gitcode.com/a/b-web",
		})
		assert.Equal(t, "https://gitcode.com/a/b", repo.ForgeURL)
	})

	t.Run("pageURL web_url when html_url empty", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:       id("1"),
			FullName: "a/b",
			WebURL:   "https://gitcode.com/a/b-web",
		})
		assert.Equal(t, "https://gitcode.com/a/b-web", repo.ForgeURL)
	})

	t.Run("forgeURL falls back to clone trimmed .git", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:       id("1"),
			FullName: "a/b",
			GitURL:   "https://gitcode.com/a/b.git",
		})
		assert.Equal(t, "https://gitcode.com/a/b", repo.ForgeURL)
	})

	t.Run("clone prefers GitURL over HTTPURL", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:         id("1"),
			FullName:   "a/b",
			GitURL:     "https://gitcode.com/a/b.git",
			GitHTTPURL: "https://gitcode.com/a/b-http.git",
			WebURL:     "https://gitcode.com/a/b",
		})
		assert.Equal(t, "https://gitcode.com/a/b.git", repo.Clone)
		assert.Equal(t, "git@gitcode.com:a/b.git", repo.CloneSSH)
	})

	t.Run("clone falls back to GitHTTPURL when GitURL empty", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:         id("1"),
			FullName:   "a/b",
			GitHTTPURL: "https://gitcode.com/a/b.git",
			WebURL:     "https://gitcode.com/a/b",
		})
		assert.Equal(t, "https://gitcode.com/a/b.git", repo.Clone)
	})

	t.Run("branch and visibility", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:            id("1"),
			FullName:      "a/b",
			DefaultBranch: "main",
			Public:        boolInt(false),
			HTMLURL:       "https://gitcode.com/a/b",
		})
		assert.Equal(t, "main", repo.Branch)
		assert.True(t, repo.IsSCMPrivate)

		repo2 := toRepo(&repository{
			ID:       id("1"),
			FullName: "a/b",
			Public:   boolInt(true),
			HTMLURL:  "https://gitcode.com/a/b",
		})
		assert.False(t, repo2.IsSCMPrivate)
	})

	t.Run("perm attached", func(t *testing.T) {
		repo := toRepo(&repository{
			ID:          id("1"),
			FullName:    "a/b",
			Permissions: &permissions{ProjectAccess: &projectAccess{AccessLevel: 50}},
			HTMLURL:     "https://gitcode.com/a/b",
		})
		require.NotNil(t, repo.Perm)
		assert.True(t, repo.Perm.Admin)
	})
}

func Test_toPerm(t *testing.T) {
	t.Run("nil permissions gives full access", func(t *testing.T) {
		perm := toPerm(&repository{Permissions: nil})
		require.NotNil(t, perm)
		assert.True(t, perm.Pull)
		assert.True(t, perm.Push)
		assert.True(t, perm.Admin)
	})

	tests := []struct {
		name            string
		level           int
		wantPull        bool
		wantPush        bool
		wantAdmin       bool
	}{
		{"level 0 guest none", 0, false, false, false},
		{"level 10 guest", 10, false, false, false},
		{"level 20 reporter pull", 20, true, false, false},
		{"level 30 developer push", 30, true, true, false},
		{"level 40 maintainer admin", 40, true, true, true},
		{"level 50 owner admin", 50, true, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			perm := toPerm(&repository{
				Permissions: &permissions{
					ProjectAccess: &projectAccess{AccessLevel: tc.level},
				},
			})
			assert.Equal(t, tc.wantPull, perm.Pull, "pull")
			assert.Equal(t, tc.wantPush, perm.Push, "push")
			assert.Equal(t, tc.wantAdmin, perm.Admin, "admin")
		})
	}

	t.Run("groupAccess takes max of project and group", func(t *testing.T) {
		tests := []struct {
			name         string
			projectLevel int
			groupLevel   int
			wantPull     bool
			wantPush     bool
			wantAdmin    bool
		}{
			{"group higher wins", 20, 40, true, true, true},
			{"project higher wins", 40, 20, true, true, true},
			{"both low", 10, 10, false, false, false},
			{"group only", 0, 30, true, true, false},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				perm := toPerm(&repository{
					Permissions: &permissions{
						ProjectAccess: &projectAccess{AccessLevel: tc.projectLevel},
						GroupAccess:   &projectAccess{AccessLevel: tc.groupLevel},
					},
				})
				assert.Equal(t, tc.wantPull, perm.Pull)
				assert.Equal(t, tc.wantPush, perm.Push)
				assert.Equal(t, tc.wantAdmin, perm.Admin)
			})
		}
	})

	t.Run("nil projectAccess groupAccess only", func(t *testing.T) {
		perm := toPerm(&repository{
			Permissions: &permissions{
				GroupAccess: &projectAccess{AccessLevel: 30},
			},
		})
		assert.True(t, perm.Pull)
		assert.True(t, perm.Push)
		assert.False(t, perm.Admin)
	})

	t.Run("both access nil yields no perms", func(t *testing.T) {
		perm := toPerm(&repository{
			Permissions: &permissions{},
		})
		assert.False(t, perm.Pull)
		assert.False(t, perm.Push)
		assert.False(t, perm.Admin)
	})
}

func Test_toTeam(t *testing.T) {
	tests := []struct {
		name      string
		ns        *namespace
		link      string
		wantLogin string
	}{
		{"simple", &namespace{Path: "myorg", AvatarURL: "https://example.com/a.png"}, "https://gitcode.com", "myorg"},
		{"relative avatar resolved", &namespace{Path: "org2", AvatarURL: "/uploads/avatar.png"}, "https://gitcode.com/org2", "org2"},
		{"empty avatar", &namespace{Path: "org3"}, "https://gitcode.com", "org3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			team := toTeam(tc.ns, tc.link)
			assert.Equal(t, tc.wantLogin, team.Login)
			// avatar is expanded; just ensure not panicking and contains host when absolute
			if tc.ns.AvatarURL == "https://example.com/a.png" {
				assert.Equal(t, "https://example.com/a.png", team.Avatar)
			}
		})
	}

	t.Run("relative avatar resolved via link", func(t *testing.T) {
		team := toTeam(&namespace{Path: "myorg", AvatarURL: "/avatar.png"}, "https://gitcode.com/myorg")
		assert.Equal(t, "https://gitcode.com/avatar.png", team.Avatar)
	})

	t.Run("CDN avatar goes through proxy", func(t *testing.T) {
		team := toTeam(&namespace{Path: "myorg", AvatarURL: "https://cdn-img.gitcode.com/bf/path.jpg"}, "https://gitcode.com")
		assert.Contains(t, team.Avatar, "/api/avatar-proxy")
		assert.Contains(t, team.Avatar, "cdn-img.gitcode.com")
	})
}

func Test_toOrg(t *testing.T) {
	tests := []struct {
		name       string
		visibility int
		wantPrivate bool
	}{
		{"0 public (visible)", 0, false},
		{"20 public", 20, false},
		{"10 private", 10, true},
		{"5 private", 5, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			org := toOrg(&namespace{Path: "myorg", VisibilityLevel: tc.visibility})
			assert.Equal(t, "myorg", org.Name)
			assert.Equal(t, tc.wantPrivate, org.Private)
		})
	}
}

func Test_pipelineFromPush(t *testing.T) {
	t.Run("full push pipeline合成", func(t *testing.T) {
		hook := &pushHook{
			Ref:   "refs/heads/main",
			After: "abc123",
			Repository: &repository{
				HTMLURL: "https://gitcode.com/a/b",
			},
			UserName:  "alice",
			UserEmail: "alice@example.com",
			Commits: []payloadCommit{
				{Message: "feat: hello", Added: []string{"a.txt"}, Modified: []string{"b.txt"}},
			},
		}
		p := pipelineFromPush(hook)
		assert.Equal(t, model.EventPush, p.Event)
		assert.Equal(t, "abc123", p.Commit)
		assert.Equal(t, "refs/heads/main", p.Ref)
		assert.Equal(t, "main", p.Branch)
		assert.Equal(t, "feat: hello", p.Message)
		assert.Equal(t, "alice", p.Author)
		assert.Equal(t, "alice", p.Sender)
		assert.Equal(t, "alice@example.com", p.Email)
		assert.Equal(t, "https://gitcode.com/a/b/commits/detail/abc123", p.ForgeURL)
		assert.Contains(t, p.Avatar, "") // just not panicking
	})

	t.Run("message fallback to push sha when commits empty", func(t *testing.T) {
		hook := &pushHook{
			Ref:   "refs/heads/main",
			After: "deadbeef",
			Repository: &repository{
				HTMLURL: "https://gitcode.com/a/b",
			},
			UserName: "bob",
		}
		p := pipelineFromPush(hook)
		assert.Equal(t, "push deadbeef", p.Message)
	})

	t.Run("author fallback chain UserName -> UserUsername -> UserEmail", func(t *testing.T) {
		tests := []struct {
			name     string
			hook     *pushHook
			wantAuthor string
		}{
			{"UserName wins", &pushHook{UserName: "alice", UserUsername: "alice2", UserEmail: "a@b.com", Repository: &repository{HTMLURL: "https://gitcode.com/a/b"}}, "alice"},
			{"UserUsername fallback", &pushHook{UserUsername: "bob", UserEmail: "b@b.com", Repository: &repository{HTMLURL: "https://gitcode.com/a/b"}}, "bob"},
			{"UserEmail fallback", &pushHook{UserEmail: "c@b.com", Repository: &repository{HTMLURL: "https://gitcode.com/a/b"}}, "c@b.com"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				p := pipelineFromPush(tc.hook)
				assert.Equal(t, tc.wantAuthor, p.Author)
				assert.Equal(t, tc.wantAuthor, p.Sender)
			})
		}
	})

	t.Run("branch stripped prefix", func(t *testing.T) {
		hook := &pushHook{Ref: "refs/heads/feature/foo", After: "sha", Repository: &repository{HTMLURL: "https://gitcode.com/a/b"}}
		p := pipelineFromPush(hook)
		assert.Equal(t, "feature/foo", p.Branch)
	})

	t.Run("changedFiles deduplicated", func(t *testing.T) {
		hook := &pushHook{
			Ref:   "refs/heads/main",
			After: "sha",
			Repository: &repository{HTMLURL: "https://gitcode.com/a/b"},
			Commits: []payloadCommit{
				{Added: []string{"a.txt", "b.txt"}, Modified: []string{"a.txt"}, Removed: []string{"c.txt"}},
			},
		}
		p := pipelineFromPush(hook)
		assert.ElementsMatch(t, []string{"a.txt", "b.txt", "c.txt"}, p.ChangedFiles)
	})

	t.Run("ForgeURL empty when no SHA or pageURL", func(t *testing.T) {
		hook := &pushHook{Ref: "refs/heads/main", After: "", Repository: &repository{}}
		p := pipelineFromPush(hook)
		assert.Equal(t, "", p.ForgeURL)
	})
}

func Test_pipelineFromTag(t *testing.T) {
	t.Run("tag pipeline Event/Ref/Branch/ForgeURL/Message/Sender synthetic", func(t *testing.T) {
		hook := &tagPushHook{
			Ref:   "refs/tags/v1.2.3",
			After: "sha1",
			Repository: &repository{HTMLURL: "https://gitcode.com/a/b"},
			Project:    &repository{HTMLURL: "https://gitcode.com/a/b"},
			UserName:   "bob",
			UserEmail:  "bob@example.com",
		}
		p := pipelineFromTag(hook)
		assert.Equal(t, model.EventTag, p.Event)
		assert.Equal(t, "sha1", p.Commit)
		assert.Equal(t, "refs/tags/v1.2.3", p.Ref)
		assert.Equal(t, "https://gitcode.com/a/b/-/tags/v1.2.3", p.ForgeURL)
		assert.Equal(t, "created tag v1.2.3", p.Message)
		assert.Equal(t, "bob", p.Author)
		assert.Equal(t, "bob", p.Sender)
		assert.Equal(t, "bob@example.com", p.Email)
	})

	t.Run("repository pageURL fallback to project", func(t *testing.T) {
		hook := &tagPushHook{
			Ref:        "refs/tags/v2.0.0",
			After:      "sha2",
			Repository: &repository{},
			Project:    &repository{HTMLURL: "https://gitcode.com/a/b"},
			UserName:   "alice",
		}
		p := pipelineFromTag(hook)
		assert.Equal(t, "https://gitcode.com/a/b/-/tags/v2.0.0", p.ForgeURL)
	})

	t.Run("bare tag ref without prefix", func(t *testing.T) {
		hook := &tagPushHook{
			Ref:        "v3.0.0",
			After:      "sha3",
			Repository: &repository{HTMLURL: "https://gitcode.com/a/b"},
			UserName:   "alice",
		}
		p := pipelineFromTag(hook)
		assert.Equal(t, "refs/tags/v3.0.0", p.Ref)
		assert.Equal(t, "https://gitcode.com/a/b/-/tags/v3.0.0", p.ForgeURL)
	})

	t.Run("author fallback UserUsername and UserEmail", func(t *testing.T) {
		hook := &tagPushHook{
			Ref:          "refs/tags/v1.0.0",
			After:        "sha",
			Repository:   &repository{HTMLURL: "https://gitcode.com/a/b"},
			UserUsername: "fallback_user",
		}
		p := pipelineFromTag(hook)
		assert.Equal(t, "fallback_user", p.Author)

		hook2 := &tagPushHook{
			Ref:        "refs/tags/v1.0.0",
			After:      "sha",
			Repository: &repository{HTMLURL: "https://gitcode.com/a/b"},
			UserEmail:  "only@email.com",
		}
		p2 := pipelineFromTag(hook2)
		assert.Equal(t, "only@email.com", p2.Author)
	})
}

func Test_pipelineFromMergeRequest(t *testing.T) {
	baseHook := func(eventType string) *mergeRequestHook {
		return &mergeRequestHook{
			EventType: eventType,
			Project:   &repository{WebURL: "https://gitcode.com/a/b"},
			ObjectAttributes: &pullRequest{
				IID:          id("42"),
				Title:        "Add feature",
				TargetBranch: "main",
				SourceBranch: "feature",
				LastCommit:   &commit{ID: "abc123"},
				Author:       &user{Username: "alice", Email: "alice@example.com", AvatarURL: "https://example.com/alice.png"},
				SourceRepo:   &repository{ID: id("1")},
				TargetRepo:   &repository{ID: id("2")},
			},
			User: &user{Username: "sender"},
			Labels: []label{{Name: "bug"}},
		}
	}

	t.Run("Event mapping", func(t *testing.T) {
		tests := []struct {
			eventType string
			wantEvent model.WebhookEvent
		}{
			{actionOpen, model.EventPull},
			{actionReopen, model.EventPull},
			{actionUpdate, model.EventPull},
			{actionClose, model.EventPullClosed},
			{actionMerge, model.EventPullClosed},
			{"unknown", model.EventPull},
		}
		for _, tc := range tests {
			t.Run(tc.eventType, func(t *testing.T) {
				hook := baseHook(tc.eventType)
				p := pipelineFromMergeRequest(hook)
				assert.Equal(t, tc.wantEvent, p.Event)
			})
		}
	})

	t.Run("Ref/Branch/Title/ForgeURL/Commit/Refspec synthetic", func(t *testing.T) {
		hook := baseHook(actionOpen)
		p := pipelineFromMergeRequest(hook)
		assert.Equal(t, "refs/pull/42/head", p.Ref)
		assert.Equal(t, "main", p.Branch)
		assert.Equal(t, "Add feature", p.Title)
		assert.Equal(t, "Add feature", p.Message)
		assert.Equal(t, "abc123", p.Commit)
		assert.Equal(t, "feature:main", p.Refspec)
		// ForgeURL fallback synthesized когда HTTPURL empty
		assert.Equal(t, "https://gitcode.com/a/b/-/merge_requests/42", p.ForgeURL)
	})

	t.Run("ForgeURL respects html_url when present", func(t *testing.T) {
		hook := baseHook(actionOpen)
		hook.ObjectAttributes.HTTPURL = "https://gitcode.com/a/b/-/merge_requests/42"
		p := pipelineFromMergeRequest(hook)
		assert.Equal(t, "https://gitcode.com/a/b/-/merge_requests/42", p.ForgeURL)
	})

	t.Run("Sender is hook.User, Author is pr.Author", func(t *testing.T) {
		hook := baseHook(actionOpen)
		p := pipelineFromMergeRequest(hook)
		assert.Equal(t, "alice", p.Author)
		assert.Equal(t, "sender", p.Sender)
		assert.Equal(t, "alice@example.com", p.Email)
	})

	t.Run("FromFork true when source != target", func(t *testing.T) {
		hook := baseHook(actionOpen)
		p := pipelineFromMergeRequest(hook)
		assert.True(t, p.FromFork)

		hook.ObjectAttributes.SourceRepo = &repository{ID: id("1")}
		hook.ObjectAttributes.TargetRepo = &repository{ID: id("1")}
		p2 := pipelineFromMergeRequest(hook)
		assert.False(t, p2.FromFork)
	})

	t.Run("labels priority hook.Labels over pr.Labels", func(t *testing.T) {
		hook := baseHook(actionOpen)
		hook.Labels = []label{{Name: "hook-label"}}
		hook.ObjectAttributes.Labels = []label{{Name: "pr-label"}}
		p := pipelineFromMergeRequest(hook)
		assert.Equal(t, []string{"hook-label"}, p.PullRequestLabels)

		hook2 := baseHook(actionOpen)
		hook2.Labels = nil
		hook2.ObjectAttributes.Labels = []label{{Name: "pr-label"}}
		p2 := pipelineFromMergeRequest(hook2)
		assert.Equal(t, []string{"pr-label"}, p2.PullRequestLabels)
	})

	t.Run("milestone and draft", func(t *testing.T) {
		hook := baseHook(actionOpen)
		hook.ObjectAttributes.Milestone = &milestone{Title: "v1.0"}
		hook.ObjectAttributes.Draft = true
		p := pipelineFromMergeRequest(hook)
		assert.Equal(t, "v1.0", p.PullRequestMilestone)
		assert.True(t, p.PullRequestDraft)
	})

	t.Run("nil LastCommit yields empty Commit", func(t *testing.T) {
		hook := baseHook(actionOpen)
		hook.ObjectAttributes.LastCommit = nil
		p := pipelineFromMergeRequest(hook)
		assert.Equal(t, "", p.Commit)
	})
}

func Test_pipelineFromRelease(t *testing.T) {
	t.Run("release Event/Ref/ForgeURL/Branch/Title/Sender synthetic", func(t *testing.T) {
		hook := &releaseHook{
			Project: &repository{WebURL: "https://gitcode.com/a/b"},
			Release: &release{
				TagName:         "v1.0.0",
				Name:            "Release v1",
				TargetCommitish: "main",
				HTMLURL:         "https://gitcode.com/a/b/-/releases/v1.0.0",
				Author:          &user{Username: "alice", Email: "a@example.com", AvatarURL: "https://example.com/a.png"},
			},
			Sender: &user{Username: "sender"},
		}
		p := pipelineFromRelease(hook)
		assert.Equal(t, model.EventRelease, p.Event)
		assert.Equal(t, "refs/tags/v1.0.0", p.Ref)
		assert.Equal(t, "https://gitcode.com/a/b/-/releases/v1.0.0", p.ForgeURL)
		assert.Equal(t, "main", p.Branch)
		assert.Equal(t, "v1.0.0", p.TagTitle)
		require.NotNil(t, p.Release)
		assert.Equal(t, "Release v1", p.Release.Title)
		assert.Equal(t, "alice", p.Author)
		assert.Equal(t, "sender", p.Sender)
	})

	t.Run("title falls back to tag when empty", func(t *testing.T) {
		hook := &releaseHook{
			Project: &repository{WebURL: "https://gitcode.com/a/b"},
			Release: &release{TagName: "v2.0.0", Name: ""},
		}
		p := pipelineFromRelease(hook)
		assert.Equal(t, "v2.0.0", p.Release.Title)
	})

	t.Run("ForgeURL synthesized when HTMLURL empty", func(t *testing.T) {
		hook := &releaseHook{
			Project: &repository{WebURL: "https://gitcode.com/a/b"},
			Release: &release{TagName: "v3.0.0", Name: "v3"},
		}
		p := pipelineFromRelease(hook)
		assert.Equal(t, "https://gitcode.com/a/b/-/releases/v3.0.0", p.ForgeURL)
	})

	t.Run("ForgeURL uses Repository when Project nil", func(t *testing.T) {
		hook := &releaseHook{
			Repository: &repository{WebURL: "https://gitcode.com/a/b"},
			Release:    &release{TagName: "v4.0.0", Name: "v4"},
		}
		p := pipelineFromRelease(hook)
		assert.Equal(t, "https://gitcode.com/a/b/-/releases/v4.0.0", p.ForgeURL)
	})

	t.Run("sender fallback hook.User then author", func(t *testing.T) {
		hook := &releaseHook{
			Project: &repository{WebURL: "https://gitcode.com/a/b"},
			Release: &release{TagName: "v5.0.0", Name: "v5", Author: &user{Username: "author1"}},
			User:    &user{Username: "hookuser"},
		}
		p := pipelineFromRelease(hook)
		assert.Equal(t, "hookuser", p.Sender)

		hook2 := &releaseHook{
			Project: &repository{WebURL: "https://gitcode.com/a/b"},
			Release: &release{TagName: "v5.0.0", Name: "v5", Author: &user{Username: "author1"}},
		}
		p2 := pipelineFromRelease(hook2)
		assert.Equal(t, "author1", p2.Sender)
	})

	t.Run("prerelease flag", func(t *testing.T) {
		hook := &releaseHook{
			Project: &repository{WebURL: "https://gitcode.com/a/b"},
			Release: &release{TagName: "v6.0.0", Name: "v6", Prerelease: true},
		}
		p := pipelineFromRelease(hook)
		assert.True(t, p.Release.IsPrerelease)
	})

	t.Run("author avatar and email from release author", func(t *testing.T) {
		hook := &releaseHook{
			Project: &repository{WebURL: "https://gitcode.com/a/b"},
			Release: &release{TagName: "v7.0.0", Name: "v7", Author: &user{Username: "alice", Email: "alice@example.com"}},
		}
		p := pipelineFromRelease(hook)
		assert.Equal(t, "alice@example.com", p.Email)
	})
}

func Test_expandAvatar(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		rawURL  string
		want    string
	}{
		{"empty stays empty", "https://gitcode.com/a/b", "", ""},
		{"absolute non-CDN unchanged", "https://gitcode.com/a/b", "https://example.com/avatar.png", "https://example.com/avatar.png"},
		{"relative slash resolved", "https://gitcode.com/a/b", "/uploads/avatar.png", "https://gitcode.com/uploads/avatar.png"},
		{"relative no slash resolved", "https://gitcode.com/a/b", "avatar.png", "https://gitcode.com/a/avatar.png"},
		{"CDN goes through proxy", "https://gitcode.com/a/b", "https://cdn-img.gitcode.com/bf/path.jpg?time=1", "/api/avatar-proxy?referer=https%3A%2F%2Fgitcode.com&url=https%3A%2F%2Fcdn-img.gitcode.com%2Fbf%2Fpath.jpg%3Ftime%3D1"},
		{"CDN with port", "https://gitcode.com/a/b", "https://cdn-img.gitcode.com/bf/ee/image.JPG?time=1705", "/api/avatar-proxy?referer=https%3A%2F%2Fgitcode.com&url=https%3A%2F%2Fcdn-img.gitcode.com%2Fbf%2Fee%2Fimage.JPG%3Ftime%3D1705"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expandAvatar(tc.repoURL, tc.rawURL)
			assert.Equal(t, tc.want, got)
		})
	}
}

func Test_sshURLFromClone(t *testing.T) {
	tests := []struct {
		name  string
		clone string
		owner string
		repo  string
		want  string
	}{
		{"https", "https://gitcode.com/a/b.git", "a", "b", "git@gitcode.com:a/b.git"},
		{"http", "http://gitcode.com/a/b.git", "a", "b", "git@gitcode.com:a/b.git"},
		{"with port", "https://gitcode.example.com:8443/a/b.git", "a", "b", "git@gitcode.example.com:8443:a/b.git"},
		{"empty clone", "", "a", "b", ""},
		{"empty owner", "https://gitcode.com/a/b.git", "", "b", ""},
		{"empty repo", "https://gitcode.com/a/b.git", "a", "", ""},
		{"no scheme with slash", "gitcode.com/a/b.git", "a", "b", "git@gitcode.com:a/b.git"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sshURLFromClone(tc.clone, tc.owner, tc.repo)
			assert.Equal(t, tc.want, got)
		})
	}
}

func Test_convertLabels(t *testing.T) {
	tests := []struct {
		name string
		from []label
		want []string
	}{
		{"empty", nil, []string{}},
		{"single", []label{{Name: "bug"}}, []string{"bug"}},
		{"multiple", []label{{Name: "bug"}, {Name: "feature"}, {Name: "wontfix"}}, []string{"bug", "feature", "wontfix"}},
		{"with color and id ignored", []label{{ID: id("1"), Name: "enhancement", Color: "#fff"}}, []string{"enhancement"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := convertLabels(tc.from)
			assert.Equal(t, tc.want, got)
		})
	}
}
