package handlers

import (
	"errors"
	"net/http"

	"github.com/gorilla/context"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/ziyadparekh/superfish/dal"
	"gopkg.in/mgo.v2/bson"
)

func getCurrentUser(w http.ResponseWriter, r *http.Request) *dal.UserModel {
	cookieStore := context.Get(r, "cookieStore").(*sessions.CookieStore)
	session, _ := cookieStore.Get(r, "superfish-session")
	return session.Values["user"].(*dal.UserModel)
}

func getIdFromPath(w http.ResponseWriter, r *http.Request) (bson.ObjectId, error) {
	userIdString := mux.Vars(r)["id"]
	if userIdString == "" {
		return "", errors.New("user id cannot be empty")
	}

	if !bson.IsObjectIdHex(userIdString) {
		return "", errors.New("user id is malformed")
	}

	oid := bson.ObjectIdHex(userIdString)

	return oid, nil
}
