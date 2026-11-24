package ginauth_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bpcoder16/baize/httpauth"
	"github.com/bpcoder16/baize/middleware/ginauth"
	"github.com/gin-gonic/gin"
)

func TestBearerRoutePolicies(t *testing.T) {
	const bearerChallenge = `Bearer realm="api"`
	unavailable := errors.New("identity service unavailable")
	guard := ginauth.New("app", httpauth.Bearer(func(_ context.Context, token string) (httpauth.Principal, error) {
		switch token {
		case "valid":
			return httpauth.Principal{UserID: "42"}, nil
		case "expired":
			return httpauth.Principal{}, fmt.Errorf("expired: %w", httpauth.ErrInvalidCredentials)
		case "unavailable":
			return httpauth.Principal{UserID: "ignored"}, unavailable
		case "missing":
			return httpauth.Principal{}, httpauth.ErrMissingCredentials
		default:
			return httpauth.Principal{UserID: " \t"}, nil
		}
	}), nil)
	router := gin.New()
	handler := func(c *gin.Context) {
		p, ok := httpauth.PrincipalFromContext(c.Request.Context())
		if !ok {
			c.String(http.StatusOK, "guest")
			return
		}
		c.String(http.StatusOK, p.Scheme+"/"+p.UserID)
	}
	router.GET("/require", guard.Require(), handler)
	router.GET("/optional", guard.Optional(), handler)
	for _, tc := range []struct {
		path      string
		headers   []string
		status    int
		body      string
		challenge string
	}{
		{"/require", nil, 401, "", bearerChallenge},
		{"/optional", nil, 200, "guest", ""},
		{"/require", []string{"Bearer valid"}, 200, "app/42", ""},
		{"/optional", []string{"Bearer valid"}, 200, "app/42", ""},
		{"/optional", []string{""}, 401, "", bearerChallenge},
		{"/optional", []string{"Basic other"}, 401, "", bearerChallenge},
		{"/optional", []string{"Bearer valid", "Bearer valid"}, 401, "", bearerChallenge},
		{"/optional", []string{"Bearer expired"}, 401, "", bearerChallenge},
		{"/optional", []string{"Bearer missing"}, 401, "", bearerChallenge},
		{"/optional", []string{"Bearer unavailable"}, 500, "", ""},
		{"/require", []string{"Bearer empty"}, 500, "", ""},
		{"/optional", []string{"Bearer empty"}, 500, "", ""},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		for _, header := range tc.headers {
			r.Header.Add("Authorization", header)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != tc.status || w.Body.String() != tc.body || w.Header().Get("WWW-Authenticate") != tc.challenge {
			t.Errorf("%s %q: status=%d body=%q challenge=%q", tc.path, tc.headers, w.Code, w.Body.String(), w.Header().Get("WWW-Authenticate"))
		}
	}
}

func TestBearerWithRealmChallenge(t *testing.T) {
	verify := func(context.Context, string) (httpauth.Principal, error) {
		t.Fatal("缺少凭证时不应调用验证器")
		return httpauth.Principal{}, nil
	}
	for _, tc := range []struct {
		realm string
		want  string
	}{
		{"admin", `Bearer realm="admin"`},
		{`运营 "desk"\ops`, `Bearer realm="运营 \"desk\"\\ops"`},
		{"", `Bearer realm=""`},
	} {
		router := gin.New()
		guard := ginauth.New("app", httpauth.BearerWithRealm(tc.realm, verify), nil)
		router.GET("/", guard.Require())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if got := w.Header().Get("WWW-Authenticate"); w.Code != http.StatusUnauthorized || got != tc.want {
			t.Errorf("realm=%q status=%d challenge=%q, want %q", tc.realm, w.Code, got, tc.want)
		}
	}
}

func TestOptionalSchemeCannotInheritAnotherIdentity(t *testing.T) {
	outer := ginauth.New("staff", httpauth.Authenticator{
		Challenge: "Session",
		Authenticate: func(*http.Request) (httpauth.Principal, bool, error) {
			return httpauth.Principal{UserID: "staff-user"}, true, nil
		},
	}, nil)
	inner := ginauth.New("app", httpauth.Bearer(func(context.Context, string) (httpauth.Principal, error) {
		t.Fatal("缺少 Bearer 凭证时不应调用验证器")
		return httpauth.Principal{}, nil
	}), nil)
	type traceKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), traceKey{}, "trace"))
	defer cancel()
	router := gin.New()
	router.GET("/", outer.Require(), inner.Optional(), func(c *gin.Context) {
		ctx := c.Request.Context()
		if _, ok := httpauth.PrincipalFromContext(ctx); ok || ctx.Value(traceKey{}) != "trace" {
			t.Fatal("游客应屏蔽外层身份并保留请求上下文")
		}
		cancel()
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("请求取消未传递")
		}
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestFailureHandlerReceivesOriginalErrorAfterAbort(t *testing.T) {
	wantErr := errors.New("backend unavailable")
	a := httpauth.Authenticator{
		Authenticate: func(*http.Request) (httpauth.Principal, bool, error) {
			return httpauth.Principal{}, false, wantErr
		},
	}
	guard := ginauth.New("session", a, func(c *gin.Context, err error) {
		if !c.IsAborted() || err != wantErr {
			t.Fatalf("aborted=%v err=%v", c.IsAborted(), err)
		}
		c.Status(http.StatusServiceUnavailable)
	})
	router := gin.New()
	router.GET("/", guard.Optional(), func(*gin.Context) { t.Fatal("失败后仍进入 handler") })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestGuardsBindIndependentIdentities(t *testing.T) {
	router := gin.New()
	for _, scheme := range []string{"app", "admin"} {
		guard := ginauth.New(scheme, httpauth.Bearer(func(_ context.Context, token string) (httpauth.Principal, error) {
			if token != scheme {
				return httpauth.Principal{}, httpauth.ErrInvalidCredentials
			}
			return httpauth.Principal{UserID: "42"}, nil
		}), nil)
		router.GET("/"+scheme, guard.Require(), func(c *gin.Context) {
			p, _ := httpauth.PrincipalFromContext(c.Request.Context())
			c.String(http.StatusOK, p.Scheme+"/"+p.UserID)
		})
	}
	for _, scheme := range []string{"app", "admin"} {
		for _, token := range []string{"app", "admin"} {
			t.Run(scheme+"/"+token, func(t *testing.T) {
				t.Parallel()
				r := httptest.NewRequest(http.MethodGet, "/"+scheme, nil)
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if token == scheme {
					if w.Code != http.StatusOK || w.Body.String() != scheme+"/42" {
						t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
					}
				} else if w.Code != http.StatusUnauthorized {
					t.Fatalf("另一方案的凭证应被拒绝: status=%d", w.Code)
				}
			})
		}
	}
}

func TestInvalidConfigurationFailsAtConstruction(t *testing.T) {
	valid := httpauth.Bearer(func(context.Context, string) (httpauth.Principal, error) {
		return httpauth.Principal{UserID: "42"}, nil
	})
	noChallenge := valid
	noChallenge.Challenge = ""
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"空方案", func() { ginauth.New("", valid, nil) }},
		{"缺少认证器", func() { ginauth.New("app", httpauth.Authenticator{}, nil) }},
		{"缺少默认挑战头", func() { ginauth.New("app", noChallenge, nil) }},
		{"缺少验证器", func() { httpauth.Bearer(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("应在构造阶段拒绝无效配置")
				}
			}()
			tc.run()
		})
	}
}
