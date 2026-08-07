package atomgit

import (
	"testing"

	"go.woodpecker-ci.org/woodpecker/v3/server/forge/atomgit/fixtures"
)

func TestDiagRepoFields(t *testing.T) {
	repo, _, err := parseHook(newHookRequestGitCodeRaw("Push Hook", fixtures.HookPushGitCode))
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if repo == nil {
		t.Fatalf("repo nil")
	}
	t.Logf("Owner=%q Name=%q FullName=%q ForgeRemoteID=%q", repo.Owner, repo.Name, repo.FullName, repo.ForgeRemoteID)
}
