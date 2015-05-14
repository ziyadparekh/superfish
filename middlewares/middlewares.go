package middlewares

import (
	"net/http"

	"github.com/gorilla/context"
	"github.com/gorilla/sessions"
	"gopkg.in/mgo.v2"
)

func SetDB(db *mgo.Session) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			context.Set(req, "db", db)
			next.ServeHTTP(res, req)
		})
	}
}

func SetCookieStore(cookieStore *sessions.CookieStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			context.Set(req, "cookieStore", cookieStore)
			next.ServeHTTP(res, req)
		})
	}
}

func MustLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		cookieStore := context.Get(req, "cookieStore").(*sessions.CookieStore)
		session, _ := cookieStore.Get(req, "superfish-session")
		userRowInterface := session.Values["user"]

		if userRowInterface == nil {
			res.WriteHeader(http.StatusNotFound)
			return
		}

		next.ServeHTTP(res, req)
	})
}
