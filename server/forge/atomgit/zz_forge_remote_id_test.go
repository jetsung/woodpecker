package atomgit

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.woodpecker-ci.org/woodpecker/v3/server/forge/atomgit/fixtures"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

// TestParsePushHookGitCodeForgeRemoteID verifies that when the "repository"
// object lacks an id (GitCode delivery), the repo id is inherited from the
// "project" object so ForgeRemoteID matches the token.
func TestParsePushHookGitCodeForgeRemoteID(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequestGitCodeRaw("Push Hook", fixtures.HookPushGitCode))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, model.ForgeRemoteID("10461881"), repo.ForgeRemoteID)
}

// TestParsePushHookGitCodeSparseRepository verifies that a GitCode-transport
// push payload, whose "repository" object is sparse (it carries only name/urls
// and omits path_with_namespace, id and namespace), still resolves owner/name
// from the richer "project" object. Without this fallback the config fetcher
// builds the URL "/api/v5/repos///file_list" (empty owner/name) and the
// pipeline 500s.
func TestParsePushHookGitCodeSparseRepository(t *testing.T) {
	repo, pipeline, err := parseHook(newHookRequestGitCodeRaw("Push Hook", fixtures.HookPushGitCode))
	assert.NoError(t, err)
	assert.NotNil(t, repo)
	assert.NotNil(t, pipeline)
	assert.Equal(t, "jetsung/testci", repo.FullName)
	assert.Equal(t, "jetsung", repo.Owner)
	assert.Equal(t, "testci", repo.Name)
	assert.Equal(t, model.ForgeRemoteID("10461881"), repo.ForgeRemoteID)
}
