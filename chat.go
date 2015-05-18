package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gopkg.in/mgo.v2/bson"
)

const (
	TokenLength int           = 32
	TtlDuration time.Duration = 20 * time.Minute
)

type User struct {
	Id       bson.ObjectId `json:"id" mgo:"_id"`
	Username string        `json:"username" mgo:"username"`
	Number   string        `json:"number" mgo:"number"`
	Token    string        `json:"token" mgo:"token"`
}

type Group struct {
	Id       bson.ObjectId `json:"id" mgo:"_id"`
	Name     string        `json:"name" mgo:"name"`
	Members  []*User       `json:"members" mgo:"members"`
	Messages []*Message    `json:"messages" mgo:"messages"`
}

type GroupPost struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type Message struct {
	Content string    `json:"content" mgo:content`
	Action  int       `json:"action" mgo:action`
	Time    time.Time `json:"time" mgo:"time"`
}

func CreateGroup(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		ServerError(w, err)
		return
	}
	group_post := new(GroupPost)
	group := new(Group)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(group_post); err != nil {
		ServerError(w, err)
	}
	if group_post.Name == "" {
		group.Name = "Group"
	}
	users, err := createMembersArray(group_post.Members)
	if err != nil {
		ServerError(w, err)
	}
	group.Messages = make([]*Message, 0)
	group.Members = users
	users = append(group.Members, curr_user)
	group.Id = bson.NewObjectId()
	// TODO:: save group to database
	gj, _ := json.Marshal(group)
	w.WriteHeader(http.StatusOK)
	w.Write(gj)
}

func currentUser(w http.ResponseWriter, r *http.Request) (*User, error) {
	if existsToken(r) {
		_, token := parseToken(r)
		user := new(User) //TODO:: Fetch user from database
		if user.Token == token {
			return user, nil
		}
		return nil, errors.New("User is not logged in")
	}
	return nil, errors.New("User is not logged in")
}

func createMembersArray(members []string) ([]*User, error) {
	users := make([]*User, 0)
	for _, u := range members {
		// TODO:: fetch users from db
		utemp := new(User)
		utemp.Username = u
		users = append(users, utemp)
	}
	return users, nil
}

func Register(w http.ResponseWriter, r *http.Request) {
	user := new(User)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(user); err != nil {
		ServerError(w, err)
		return
	}
	user.Id = bson.NewObjectId()
	user.Token, _ = generateUserToken(user)
	// TODO:: save user to db
	uj, _ := json.Marshal(user)
	w.WriteHeader(http.StatusOK)
	w.Write(uj)
}

//ServerError handles server errors writing a response to the client and logging the error.
func ServerError(w http.ResponseWriter, err error) {
	log.Println(err)
	w.Header().Set("success", "false")
	w.Header().Set("code", "50")
	w.WriteHeader(http.StatusInternalServerError)
}

func existsToken(rq *http.Request) bool {
	token := rq.Header.Get("Authorization")
	if token != "" {
		return true
	}
	return false
}

func parseToken(rq *http.Request) (u, t string) {
	token := rq.Header.Get("Authorization")
	if token == "" {
		return "", ""
	}
	parts := strings.Split(token, "_")
	if len(parts) == 2 {
		return parts[0], parts[1]
	} else {
		return parts[0], ""
	}
}

func generateUserToken(u *User) (string, error) {
	token, err := generateFreshToken()
	if err != nil {
		return "", err
	}
	userToken := u.Id.Hex() + "_" + token
	return userToken, nil
}

func generateFreshToken() (string, error) {
	token := make([]byte, TokenLength)
	if _, err := io.ReadFull(rand.Reader, token); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(token), nil
}

func resetTokenExpiryTime() time.Time {
	return time.Now().UTC().Add(TtlDuration)
}

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/signup", Register).Methods("POST")
	router.HandleFunc("/group", CreateGroup).Methods("POST")

	err := http.ListenAndServe(":8080", router)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
