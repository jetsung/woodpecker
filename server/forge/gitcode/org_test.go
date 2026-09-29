package gitcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

// Test_gitcode_org_enterprise reproduces the add-organization-repo 500:
// /users/{name} covers personal accounts only and answers 404
// ("用户不存在") for an enterprise namespace, so Org() must fall back to
// /orgs/{name}.
func Test_gitcode_org_enterprise(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log.Logger = log.Output(gin.DefaultWriter)

	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Enterprise namespace: /users 404s, /orgs serves the enterprise payload.
	mux.HandleFunc("/api/v5/users/fork", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":1003,"error_code_name":"NOT_EXIST","error_message":"用户不存在"}`))
	})
	mux.HandleFunc("/api/v5/orgs/fork", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":8113384,"login":"fork","name":"Fork","path":"fork","html_url":"https://gitcode.com/fork","public":true}`))
	})

	c := &GitCode{url: ts.URL}
	org, err := c.Org(context.Background(), &model.User{AccessToken: "x"}, "fork")
	if err != nil {
		t.Fatalf("Org() error: %v", err)
	}
	if org.Name != "fork" {
		t.Fatalf("org name = %q, want fork", org.Name)
	}
}
