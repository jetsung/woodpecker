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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"go.woodpecker-ci.org/woodpecker/v3/server/forge/types"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

const (
	hookEvent        = "X-AtomGit-Event"
	hookEventGitCode = "X-GitCode-Event"
	hookUserAgent    = "User-Agent"

	// userAgentGitCode is the User-Agent AtomGit sends on webhook deliveries via
	// the GitCode transport. It is used as a fallback event-source signal when
	// the X-*-Event header is absent.
	userAgentGitCode = "git-gitcode-hook"

	hookPush         = "push"
	hookTagPush      = "tag_push"
	hookMergeRequest = "merge_request"

	actionOpen   = "merge_request_open"
	actionClose  = "merge_request_close"
	actionMerge  = "merge_request_merge"
	actionUpdate = "merge_request_update"
	actionReopen = "merge_request_reopen"

	refBranch = "branch"
	refTag    = "tag"
)

// hookEventType resolves the AtomGit webhook event type from the request.
// Webhooks may arrive with either the X-AtomGit-Event or the X-GitCode-Event
// header; as a last resort a GitCode User-Agent header is treated as a push.
func hookEventType(r *http.Request) string {
	var event string
	if e := r.Header.Get(hookEvent); e != "" {
		event = e
	} else if e := r.Header.Get(hookEventGitCode); e != "" {
		event = e
	} else if strings.Contains(strings.ToLower(r.Header.Get(hookUserAgent)), userAgentGitCode) {
		// Fallback: GitCode identifies hook deliveries via the User-Agent header.
		return hookPush
	} else {
		return ""
	}

	// Normalize header values such as "Push Hook" to the internal lowercase
	// event types; already-lowercase values pass through unchanged.
	switch {
	case strings.EqualFold(event, "Push Hook"):
		return hookPush
	case strings.EqualFold(event, "Tag Push Hook"):
		return hookTagPush
	case strings.EqualFold(event, "Merge Request Hook"):
		return hookMergeRequest
	}
	return event
}

// parseHook parses a AtomGit webhook from an http.Request and returns the
// Repo and Pipeline detail. If a hook type is unsupported nil values are returned.
func parseHook(r *http.Request) (*model.Repo, *model.Pipeline, error) {
	hookType := hookEventType(r)
	switch hookType {
	case hookPush:
		return parsePushHook(r.Body)
	case hookTagPush:
		return parseTagPushHook(r.Body)
	case hookMergeRequest:
		return parseMergeRequestHook(r.Body)
	}
	log.Debug().Msgf("unsupported atomgit hook type: '%s'", hookType)
	return nil, nil, &types.ErrIgnoreEvent{Event: hookType}
}

func parsePushHook(payload io.Reader) (*model.Repo, *model.Pipeline, error) {
	hook := new(pushHook)
	if err := json.NewDecoder(payload).Decode(hook); err != nil {
		return nil, nil, err
	}

	if hook.Repository == nil {
		// The repository may be carried under "project" and omit "repository"
		// entirely, so fall back to it.
		if hook.Project != nil {
			hook.Repository = hook.Project
		} else if hook.ProjectID != "" {
			return nil, nil, fmt.Errorf("parsed push webhook does not contain repository info")
		} else {
			return nil, nil, fmt.Errorf("parsed push webhook does not contain repository info")
		}
	}

	// The "repository" object is often sparse (it omits "path_with_namespace",
	// "id" and "namespace"); enrich it from the richer "project" object.
	if hook.Project != nil {
		if hook.Repository.PathWithNamespace == "" {
			hook.Repository.PathWithNamespace = hook.Project.PathWithNamespace
		}
		if hook.Repository.FullName == "" {
			hook.Repository.FullName = hook.Project.FullName
		}
		if hook.Repository.Namespace == nil {
			hook.Repository.Namespace = hook.Project.Namespace
		}
		if hook.Repository.Name == "" {
			hook.Repository.Name = hook.Project.Name
		}
		if hook.Repository.ID == "" {
			if hook.Project.ID != "" {
				hook.Repository.ID = hook.Project.ID
			} else if hook.ProjectID != "" {
				hook.Repository.ID = hook.ProjectID
			}
		}
		if hook.Repository.DefaultBranch == "" {
			hook.Repository.DefaultBranch = hook.Project.DefaultBranch
		}
	}

	// ignore tag pushes handled by tag_push event
	if strings.HasPrefix(hook.Ref, "refs/tags/") {
		return nil, nil, nil
	}

	repo := toRepo(hook.Repository)
	pipeline := pipelineFromPush(hook)
	return repo, pipeline, nil
}

func parseTagPushHook(payload io.Reader) (*model.Repo, *model.Pipeline, error) {
	hook := new(tagPushHook)
	if err := json.NewDecoder(payload).Decode(hook); err != nil {
		return nil, nil, err
	}

	if hook.Repository == nil {
		if hook.Project != nil {
			hook.Repository = hook.Project
		} else {
			return nil, nil, fmt.Errorf("parsed tag push webhook does not contain repository info")
		}
	}

	if hook.Project != nil {
		if hook.Repository.PathWithNamespace == "" {
			hook.Repository.PathWithNamespace = hook.Project.PathWithNamespace
		}
		if hook.Repository.FullName == "" {
			hook.Repository.FullName = hook.Project.FullName
		}
		if hook.Repository.Namespace == nil {
			hook.Repository.Namespace = hook.Project.Namespace
		}
		if hook.Repository.Name == "" {
			hook.Repository.Name = hook.Project.Name
		}
		if hook.Repository.ID == "" {
			if hook.Project.ID != "" {
				hook.Repository.ID = hook.Project.ID
			} else if hook.ProjectID != "" {
				hook.Repository.ID = hook.ProjectID
			}
		}
		if hook.Repository.DefaultBranch == "" {
			hook.Repository.DefaultBranch = hook.Project.DefaultBranch
		}
	}

	repo := toRepo(hook.Repository)
	pipeline := pipelineFromTag(hook)
	return repo, pipeline, nil
}

func parseMergeRequestHook(payload io.Reader) (*model.Repo, *model.Pipeline, error) {
	hook := new(mergeRequestHook)
	if err := json.NewDecoder(payload).Decode(hook); err != nil {
		return nil, nil, err
	}

	if hook.ObjectAttributes == nil {
		return nil, nil, fmt.Errorf("parsed merge_request webhook does not contain merge request info")
	}

	if !supportedMergeRequestAction(hook.EventType) {
		log.Debug().Msgf("merge_request action '%s' is not supported, ignoring", hook.EventType)
		return nil, nil, nil
	}

	repo := toRepo(hook.Project)
	pipeline := pipelineFromMergeRequest(hook)
	return repo, pipeline, nil
}

func supportedMergeRequestAction(action string) bool {
	switch action {
	case actionOpen, actionUpdate, actionReopen, actionClose, actionMerge:
		return true
	default:
		return false
	}
}
