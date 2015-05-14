package handlers

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/context"
	"github.com/gorilla/sessions"
	"github.com/ziyadparekh/superfish/dal"
	"github.com/ziyadparekh/superfish/libhttp"
	"gopkg.in/mgo.v2"
)

type Signup struct {
	Number   string `json:"number"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type copyReader struct {
	*bytes.Buffer
}

// So that it implements the io.ReadCloser interface
func (m copyReader) Close() error {
	return nil
}

func GetLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cookieStore := context.Get(r, "cookieStore").(*sessions.CookieStore)

	session, _ := cookieStore.Get(r, "superfish-session")

	currentUserInterface := session.Values["user"]
	if currentUserInterface != nil {
		w.WriteHeader(301)
		return
	}
	w.WriteHeader(200)
	return
}

func PostSignup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	s := Signup{}
	buf, _ := ioutil.ReadAll(r.Body)
	// Need to make a copy of request body
	rdr1 := copyReader{bytes.NewBuffer(buf)}
	rdr2 := copyReader{bytes.NewBuffer(buf)}

	decoder := json.NewDecoder(rdr1)
	err := decoder.Decode(&s)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	db := context.Get(r, "db").(*mgo.Session)

	db_err := dal.NewUser(db).Signup(s.Number, s.Username, s.Password)
	if db_err != nil {
		logrus.Error(db_err.Error())
		libhttp.HandleErrorJson(w, db_err)
		return
	}
	r.Body = rdr2
	PostLogin(w, r)
}

func PostLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	db := context.Get(r, "db").(*mgo.Session)
	cookieStore := context.Get(r, "cookieStore").(*sessions.CookieStore)

	s := Signup{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&s)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	u := dal.NewUser(db)

	user, err := u.GetUserByUsernameAndPassword(s.Username, s.Password)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	session, _ := cookieStore.Get(r, "superfish-session")
	session.Values["user"] = user

	s_err := session.Save(r, w)
	if s_err != nil {
		libhttp.HandleErrorJson(w, s_err)
		return
	}

	uj, _ := json.Marshal(user)
	w.WriteHeader(http.StatusOK)
	w.Write(uj)

	return
}
