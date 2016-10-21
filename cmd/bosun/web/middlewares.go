package web

import (
	"net/http"

	"github.com/MiniProfiler/go/miniprofiler"
	"github.com/captncraig/easyauth"
	"github.com/captncraig/easyauth/providers/ldap"
	"github.com/captncraig/easyauth/providers/token"
	"github.com/gorilla/mux"

	"bosun.org/cmd/bosun/conf"
	"bosun.org/collect"
	"bosun.org/metadata"
	"bosun.org/opentsdb"
)

// custom middlewares for bosun. Must match  alice.Constructor signature (func(http.Handler) http.Handler)

var miniprofilerMiddleware = func(next http.Handler) http.Handler {
	return miniprofiler.NewContextHandler(next.ServeHTTP)
}

var endpointStatsMiddleware = func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//metric for http vs https
		proto := "http"
		if r.TLS != nil {
			proto = "https"
		}
		collect.Add("bosun.http_protocol", opentsdb.TagSet{"proto": proto}, 1)

		//if we use gorilla named routes, we can add stats and timings per route
		routeName := ""
		if route := mux.CurrentRoute(r); route != nil {
			routeName = route.GetName()
		}
		if routeName == "" {
			routeName = "unknown"
		}
		t := collect.StartTimer("bosun.http_routes", opentsdb.TagSet{"route": routeName})
		next.ServeHTTP(w, r)
		t()
	})
}

func buildAuth(cfg *conf.AuthConf) (easyauth.AuthManager, *token.TokenProvider, error) {

	if cfg == nil {
		return nil, nil, nil
	}
	auth, err := easyauth.New(easyauth.CookieSecret("ASDASDASDASDASD"))
	if err != nil {
		return nil, nil, err
	}

	l, err := buildLDAPConfig(cfg.LDAP)
	if err != nil {
		return nil, nil, err
	}
	auth.AddProvider("ldap", l)

	// in proc token store so bosun can send data to itself with an ephemeral token
	selfStore, _ := token.NewJsonStore("")
	selfToken := token.NewToken(easyauth.RandomString(16), selfStore)
	tok, _ := selfToken.NewToken("bosun", "internal bosun token", canPutData)
	collect.AuthToken = tok
	metadata.AuthToken = tok
	auth.AddProvider("self", selfToken)

	var authTokens *token.TokenProvider
	if cfg.TokenSecret != "" {
		tokData, err := token.NewJsonStore("tokens.json") //TODO: redis once pr merged
		if err != nil {
			return nil, nil, err
		}
		authTokens = token.NewToken(cfg.TokenSecret, tokData)
		auth.AddProvider("tok", authTokens)
	}
	return auth, authTokens, nil
}

func buildLDAPConfig(ld conf.LDAPConf) (*ldap.LdapProvider, error) {
	l := &ldap.LdapProvider{
		Domain:         ld.Domain,
		LdapAddr:       ld.LdapAddr,
		AllowInsecure:  ld.AllowInsecure,
		RootSearchPath: ld.RootSearchPath,
		Users:          map[string]easyauth.Role{},
	}
	var role easyauth.Role
	var err error
	if role, err = parseRole(ld.DefaultPermission); err != nil {
		return nil, err
	}
	l.DefaultPermission = role
	for _, g := range ld.Groups {
		if role, err = parseRole(g.Role); err != nil {
			return nil, err
		}
		l.Groups = append(l.Groups, &ldap.LdapGroup{Path: g.Path, Role: role})
	}
	for name, perm := range ld.Users {
		if role, err = parseRole(perm); err != nil {
			return nil, err
		}
		l.Users[name] = role
	}
	return l, nil
}