package httpauth_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bpcoder16/baize/httpauth"
)

func TestBearerToken(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		token   string
		wantErr error
	}{
		{"缺失", nil, "", httpauth.ErrMissingCredentials},
		{"合法字符和填充", []string{"bEaReR   Az09-._~+/=="}, "Az09-._~+/==", nil},
		{"空头", []string{""}, "", httpauth.ErrInvalidCredentials},
		{"只有空白", []string{"  "}, "", httpauth.ErrInvalidCredentials},
		{"无令牌", []string{"Bearer "}, "", httpauth.ErrInvalidCredentials},
		{"其他方式", []string{"Basic abc"}, "", httpauth.ErrInvalidCredentials},
		{"重复头", []string{"Bearer abc", "Bearer def"}, "", httpauth.ErrInvalidCredentials},
		{"逗号", []string{"Bearer abc,def"}, "", httpauth.ErrInvalidCredentials},
		{"额外字段", []string{"Bearer abc def"}, "", httpauth.ErrInvalidCredentials},
		{"制表符分隔", []string{"Bearer\tabc"}, "", httpauth.ErrInvalidCredentials},
		{"非 ASCII 空白", []string{"Bearer\u00a0abc"}, "", httpauth.ErrInvalidCredentials},
		{"中间填充", []string{"Bearer a=b"}, "", httpauth.ErrInvalidCredentials},
		{"只有填充", []string{"Bearer =="}, "", httpauth.ErrInvalidCredentials},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/?access_token=other", nil)
			r.AddCookie(&http.Cookie{Name: "access_token", Value: "other"})
			for _, header := range tc.headers {
				r.Header.Add("Authorization", header)
			}
			token, err := httpauth.BearerToken(r)
			if token != tc.token || !errors.Is(err, tc.wantErr) {
				t.Fatalf("token=%q err=%v", token, err)
			}
		})
	}
}

func TestBearerPassesRequestContextToVerifier(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := httpauth.Bearer(func(got context.Context, token string) (httpauth.Principal, error) {
		if got != ctx || token != "valid" {
			t.Fatal("验证器应收到请求上下文和提取的凭证")
		}
		return httpauth.Principal{}, got.Err()
	})
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer valid")
	_, _, err := a.Authenticate(r)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误丢失: %v", err)
	}
}

func TestBearerRejectsEmptyIdentityWithoutGin(t *testing.T) {
	a := httpauth.Bearer(func(context.Context, string) (httpauth.Principal, error) {
		return httpauth.Principal{UserID: " \t"}, nil
	})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer valid")
	p, present, err := a.Authenticate(r)
	if !present || p != (httpauth.Principal{}) || err == nil || errors.Is(err, httpauth.ErrInvalidCredentials) {
		t.Fatalf("验证器返回空白身份应报告内部错误: p=%+v present=%v err=%v", p, present, err)
	}
}

func TestBearerWithRealmRejectsInvalidHeaderBytes(t *testing.T) {
	verify := func(context.Context, string) (httpauth.Principal, error) {
		return httpauth.Principal{}, httpauth.ErrInvalidCredentials
	}
	for _, realm := range []string{"a\rb", "a\nb", "a\x00b", "a\x7fb"} {
		t.Run(fmt.Sprintf("%q", realm), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("构造时应拒绝头字段不允许的字符")
				}
			}()
			httpauth.BearerWithRealm(realm, verify)
		})
	}
}
