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

package fixtures

// HookPush is a AtomGit push webhook payload.
const HookPush = `{
  "object_kind": "push",
  "event_name": "push",
  "before": "95790bf891e76feeb30fa2fcc762bd98c1e28ad9",
  "after": "da1560886d4f094c3e6c9ef40349f7d38b5d27d7",
  "ref": "refs/heads/master",
  "checkout_sha": "da1560886d4f094c3e6c9ef40349f7d38b5d27d7",
  "user_id": 4,
  "user_name": "John Doe",
  "user_email": "john@example.com",
  "user_avatar": "https://atomgit.com/avatar.png",
  "project_id": 15,
  "project": {
    "id": 15,
    "name": "repo_name",
    "path_with_namespace": "test_name/repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://atomgit.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://atomgit.com/test_name/repo_name"
  },
  "repository": {
    "id": 15,
    "name": "repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://atomgit.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://atomgit.com/test_name/repo_name"
  },
  "commits": [
    {
      "id": "da1560886d4f094c3e6c9ef40349f7d38b5d27d7",
      "message": "fix: bugs",
      "title": "fix: bugs",
      "url": "https://atomgit.com/test_name/repo_name/-/commit/da1560886d4f094c3e6c9ef40349f7d38b5d27d7",
      "author": {"name": "John Doe", "email": "john@example.com", "username": "john"},
      "added": ["file1.txt"],
      "modified": ["file2.txt"],
      "removed": []
    }
  ],
  "total_commits_count": 1
}`

// HookTagPush is a AtomGit tag push webhook payload.
const HookTagPush = `{
  "object_kind": "tag_push",
  "event_name": "tag_push",
  "before": "0000000000000000000000000000000000000000",
  "after": "82b3d5ae55f7080f1e6022629cdb57bfae7cccc7",
  "ref": "refs/tags/v1.0.0",
  "checkout_sha": "82b3d5ae55f7080f1e6022629cdb57bfae7cccc7",
  "user_id": 4,
  "user_name": "John Doe",
  "user_email": "john@example.com",
  "user_avatar": "https://atomgit.com/avatar.png",
  "project_id": 15,
  "project": {
    "id": 15,
    "name": "repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://atomgit.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://atomgit.com/test_name/repo_name"
  },
  "repository": {
    "id": 15,
    "name": "repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://atomgit.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://atomgit.com/test_name/repo_name"
  },
  "commits": [],
  "total_commits_count": 0
}`

// HookPushGitCode is a webhook payload delivered via the GitCode transport
// (X-GitCode-Event header, "git-gitcode-hook" User-Agent, user_username field,
// and a repository object keyed by git_http_url / git_ssh_url / web_url /
// homepage), so it must be parsed identically to HookPush.
const HookPushGitCode = `{
  "object_kind": "push",
  "event_name": "push",
  "before": "3dcdc08b89e35c59eb304ae89a8cd64ba719cf3c",
  "after": "8b1218282d9a5745ea816c280e00063fdc39eabf",
  "ref": "refs/heads/main",
  "checkout_sha": "8b1218282d9a5745ea816c280e00063fdc39eabf",
  "message": "",
  "user_id": 143790,
  "user_name": "jetsung",
  "user_username": "jetsung",
  "user_email": "",
  "user_avatar": "https://cdn-img.gitcode.com/bf/ee/b4f489e3933733e085b0f6ad0073345142d939d9e53c67d7e9445c66b71ad0a0.JPG?time=1705574695777",
  "project_id": 10461881,
  "project": {
    "id": 10461881,
    "name": "testci",
    "description": "",
    "web_url": "https://gitcode.com/jetsung/testci",
    "avatar_url": "https://cdn-img.gitcode.com/bf/ee/b4f489e3933733e085b0f6ad0073345142d939d9e53c67d7e9445c66b71ad0a0.JPG?time=1705574695777",
    "git_ssh_url": "git@gitcode.com:jetsung/testci.git",
    "git_http_url": "https://gitcode.com/jetsung/testci.git",
    "namespace": "jetsung",
    "visibility_level": 20,
    "path_with_namespace": "jetsung/testci",
    "default_branch": "main",
    "homepage": "https://gitcode.com/jetsung/testci",
    "url": "git@gitcode.com:jetsung/testci.git",
    "ssh_url": "git@gitcode.com:jetsung/testci.git",
    "http_url": "https://gitcode.com/jetsung/testci.git"
  },
  "commits": [
    {
      "id": "8b1218282d9a5745ea816c280e00063fdc39eabf",
      "message": "testci\n",
      "timestamp": "2026-07-29T17:42:42Z",
      "url": "https://gitcode.com/jetsung/testci/commits/detail/8b1218282d9a5745ea816c280e00063fdc39eabf",
      "author": {
        "name": "Jetsung Chan",
        "email": "jetsungchan@gmail.com"
      },
      "added": [],
      "removed": [],
      "modified": ["README.md"],
      "cooperate_authors": [
        {"name": "Jetsung Chan", "email": "jetsungchan@gmail.com"}
      ]
    }
  ],
  "total_commits_count": 1,
  "push_options": [],
  "repository": {
    "name": "testci",
    "url": "git@gitcode.com:jetsung/testci.git",
    "description": "",
    "homepage": "https://gitcode.com/jetsung/testci",
    "git_http_url": "https://gitcode.com/jetsung/testci.git",
    "git_ssh_url": "git@gitcode.com:jetsung/testci.git",
    "visibility_level": 20
  },
  "git_branch": "main",
  "git_commit_no": "8b1218282d9a5745ea816c280e00063fdc39eabf",
  "manual_build": false,
  "uuid": "65a70057-7c95-49c9-b8b4-31ef0b040722",
  "hook_id": 64665,
  "hook_type": "project"
}`

// HookMergeRequest is a AtomGit merge request (open) webhook payload.
const HookMergeRequest = `{
  "object_kind": "merge_request",
  "event_name": "merge_request_open",
  "user": {
    "id": 1,
    "username": "someuser",
    "name": "Some User",
    "email": "someuser@atomgit.com",
    "avatar_url": "https://atomgit.com/avatar.png",
    "html_url": "https://atomgit.com/someuser"
  },
  "project": {
    "id": 15,
    "name": "repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://atomgit.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://atomgit.com/test_name/repo_name"
  },
  "repository": {
    "id": 15,
    "name": "repo_name",
    "full_name": "test_name/repo_name",
    "http_url_to_repo": "https://atomgit.com/test_name/repo_name.git",
    "default_branch": "main",
    "html_url": "https://atomgit.com/test_name/repo_name"
  },
  "object_attributes": {
    "id": 99,
    "iid": 1,
    "target_branch": "master",
    "source_branch": "feature",
    "title": "Add feature",
    "state": "opened",
    "html_url": "https://atomgit.com/test_name/repo_name/-/merge_requests/1",
    "author": {
      "id": 1,
      "username": "someuser",
      "name": "Some User",
      "email": "someuser@atomgit.com",
      "avatar_url": "https://atomgit.com/avatar.png"
    },
    "source_repo": {
      "id": 15,
      "full_name": "test_name/repo_name"
    },
    "target_repo": {
      "id": 15,
      "full_name": "test_name/repo_name"
    },
    "draft": false,
    "last_commit": {
      "id": "da1560886d4f094c3e6c9ef40349f7d38b5d27d7"
    }
  },
  "labels": [
    {"id": 1, "name": "bug", "color": "#d9534f"}
  ]
}`
