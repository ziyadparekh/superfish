package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/carbocation/interpose"
	"github.com/gorilla/context"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	TokenLength int           = 32
	TtlDuration time.Duration = 20 * time.Minute
)

var ErrClientExists = errors.New("clientdata: Client already exists")
var ErrUsernameInvalid = errors.New("Error registering user: Invalid username")
var ErrPasswordInvalid = errors.New("Error registering user: Invalid password")
var ErrNumberInvalid = errors.New("Error registering user: Invalid number")

type User struct {
	Id       bson.ObjectId `json:"id" mgo:"_id"`
	Username string        `json:"username" mgo:"username"`
	Number   string        `json:"number" mgo:"number"`
	Token    string        `json:"token" mgo:"token"`
	Password string        `json:"password" mgo:"password"`
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

type Base struct {
	db         *mgo.Session
	collection string
	dbName     string
}

type UserDataClient struct {
	Base
}

func NewUserData(db *mgo.Session) *UserDataClient {
	u := new(UserDataClient)
	u.db = db
	u.collection = "users"
	u.dbName = "superfish"
	return u
}

func (u *UserDataClient) GetByUsername(username string) (*User, error) {
	user := new(User)
	c := u.db.DB(u.dbName).C(u.collection)
	err := c.Find(bson.M{"username": username}).One(user)
	return user, err
}

func (u *UserDataClient) CreateNewUser(user *User) error {
	c := u.db.DB(u.dbName).C(u.collection)
	err := c.Insert(user)
	return err
}

func (u *UserDataClient) ClientExists(username string) bool {
	_, err := u.GetByUsername(username)
	if err == mgo.ErrNotFound {
		return false
	}
	return true
}

func (u *UserDataClient) NewUser(user *User) error {
	exists := u.ClientExists(user.Username)
	if exists {
		return ErrClientExists
	}
	user.Password = Encrypt(user.Password)
	err := u.CreateNewUser(user)
	if err != nil {
		return err
	}
	return nil
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
	if err := ValidateUser(user); err != nil {
		w.Header().Set("success", "false")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	user.Id = bson.NewObjectId()
	user.Token, _ = generateUserToken(user)
	db := GetMongoSession(r)
	u := NewUserData(db)
	err := u.NewUser(user)
	switch {
	case err == ErrClientExists:
		log.Println(err)
		w.Header().Set("success", "false")
		w.Header().Set("code", "20")
		w.Write([]byte(err.Error()))
	case err != nil:
		ServerError(w, err)
	default:
		uj, _ := json.Marshal(user)
		w.WriteHeader(http.StatusOK)
		w.Write(uj)
	}
}

func ValidateUser(u *User) error {
	if !ValidateName(u.Username) {
		return ErrUsernameInvalid
	}
	if !ValidatePassword(u.Password) {
		return ErrPasswordInvalid
	}
	if !ValidateNumber(u.Number) {
		return ErrNumberInvalid
	}
	return nil
}

//ValidateName returns true if a name is only alphanumeric characters.
func ValidateName(name string) bool {
	inv, err := regexp.MatchString("[^[:alnum:]]", name)
	if err != nil {
		log.Println("Error in ValidateName: ", err)
	}
	return !inv
}

func ValidatePassword(pswd string) bool {
	if pswd == "" || len([]rune(pswd)) < 3 {
		return false
	}
	return true
}

func ValidateNumber(number string) bool {
	reg := regexp.MustCompile(`^(\([0-9]{3}\)|[0-9]{3})[0-9]{3}[0-9]{4}$`)
	if number == "" || !reg.MatchString(number) {
		return false
	}
	return true
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

//encrypt encrypts the password and returns the encrypted version.
func Encrypt(pword string) string {
	crypass, err := bcrypt.GenerateFromPassword([]byte(pword), 12)
	if err != nil {
		log.Println("Error encrypting pass: ", err)
	}
	return string(crypass)
}

func resetTokenExpiryTime() time.Time {
	return time.Now().UTC().Add(TtlDuration)
}

func init() {
	gob.Register(new(User))
}

func GetMongoSession(r *http.Request) *mgo.Session {
	return context.Get(r, "db").(*mgo.Session)
}

func getDb() *mgo.Session {
	dsn := "mongodb://localhost"
	db, err := mgo.Dial(dsn)
	if err != nil {
		log.Println(err)
		return nil
	}
	return db
}

func SetDB(db *mgo.Session) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			context.Set(req, "db", db)
			next.ServeHTTP(res, req)
		})
	}
}

func middlewareStruct() *interpose.Middleware {
	middle := interpose.New()
	middle.Use(SetDB(getDb()))
	return middle
}

func routeMux() *mux.Router {
	router := mux.NewRouter()
	router.HandleFunc("/signup", Register).Methods("POST")
	router.HandleFunc("/group", CreateGroup).Methods("POST")
	return router
}

func main() {
	router := routeMux()
	middle := middlewareStruct()
	middle.UseHandler(router)

	err := http.ListenAndServe(":8080", middle)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
