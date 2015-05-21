package main

import (
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/carbocation/interpose"
	"github.com/gorilla/context"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
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
var ErrGroupNameInvalid = errors.New("Error creating new group: Group name must be alphanumeric")
var ErrUnauthorizedAccess = errors.New("Unauthorized access")

var Upgrader *websocket.Upgrader
var ActiveGroups = make(map[string]*RealTimeGroup)

type User struct {
	Id       bson.ObjectId `json:"id" bson:"_id"`
	Username string        `json:"username" bson:"username"`
	Number   string        `json:"number" bson:"number"`
	Token    string        `json:"token" bson:"token"`
	Password string        `json:"password" bson:"password"`
}

type RealTimeUser struct {
	User         *User           `json:"user"`
	Ws           *websocket.Conn `json:"-"`
	SendQueue    chan []byte     `json:"-"`
	GroupId      string          `json:"group_id"`
	LastActivity time.Time       `json:"last_activity"`
}

type Group struct {
	Id       bson.ObjectId `json:"id" bson:"_id"`
	Name     string        `json:"name" bson:"name"`
	Members  []User        `json:"members" bson:"members"`
	Messages []Message     `json:"messages" bson:"messages"`
}

type RealTimeGroup struct {
	Group      *Group `json:"group"`
	DataClient *GroupDataClient
	Users      map[*RealTimeUser]bool `json:"-"`
	Broadcast  chan []byte            `json:"-"`
	Register   chan *RealTimeUser     `json:"-"`
	Unregister chan *RealTimeUser     `json:"-"`
	Stop       chan int               `json:"-"`
}

type RealTimeMessage struct {
	Sender  string    `json:"sender"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Group   string    `json:"group"`
}

type GroupPost struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type Message struct {
	Content string    `json:"content" bson:content`
	Action  int       `json:"action" bson:action`
	Time    time.Time `json:"time" bson:"time"`
}

type Base struct {
	db         *mgo.Session
	collection string
	dbName     string
}

type UserDataClient struct {
	Base
}

type GroupDataClient struct {
	Base
}

func (rt_group *RealTimeGroup) Run() {
	log.Println("Starting room ", rt_group.Group.Id.Hex())
loop:
	for {
		select {
		case rtu, ok := <-rt_group.Register:
			if ok {
				rt_group.Users[rtu] = true
				log.Println("User is live in group", rtu.User.Username)
			} else {
				break loop
			}
		case rtu, ok := <-rt_group.Unregister:
			if ok {
				if _, ok := rt_group.Users[rtu]; ok {
					delete(rt_group.Users, rtu)
					close(rtu.SendQueue)
					log.Println("User has disconnected from group", rtu.User.Username)
				}
			}
		case m, ok := <-rt_group.Broadcast:
			if ok {
				for user := range rt_group.Users {
					msg := new(RealTimeMessage)
					json.Unmarshal(m, msg)
					if msg.Sender != user.User.Username {
						select {
						case user.SendQueue <- []byte(msg.Content):
						default:
							close(user.SendQueue)
							delete(rt_group.Users, user)
						}
					}
				}
			} else {
				break loop
			}
		case _ = <-rt_group.Stop:
			break loop
		}
	}
}

func (rt_group *RealTimeGroup) Announce(message *RealTimeMessage, u *RealTimeUser) {
	if len(rt_group.Users) > 0 {
		rt_group.Broadcast <- PrepareMessage(message)
		if err := rt_group.AddMessageToDatabase(message); err != nil {
			log.Println(err)
		}
	}
}

func (rt_group *RealTimeGroup) AddMessageToDatabase(message *RealTimeMessage) error {
	C := rt_group.DataClient.db.DB(rt_group.DataClient.dbName).C("messages")
	err := C.Insert(message)
	return err
}

func (rt_user *RealTimeUser) Write(mt int, payload []byte) error {
	return rt_user.Ws.WriteMessage(mt, payload)
}

func (rt_user *RealTimeUser) Talk() {
	defer func() {
		rt_user.Ws.Close()
	}()
	for {
		select {
		case message, ok := <-rt_user.SendQueue:
			if !ok {
				rt_user.Write(websocket.CloseMessage, []byte{})
				return
			}
			if err := rt_user.Write(websocket.TextMessage, message); err != nil {
				return
			}
		}
	}
}

func (rt_user *RealTimeUser) Listen() {
	defer func() {
		if _, exists := ActiveGroups[rt_user.GroupId]; exists {
			ActiveGroups[rt_user.GroupId].Unregister <- rt_user
		}
		rt_user.Ws.Close()
	}()
	for {
		_, message, err := rt_user.Ws.ReadMessage()
		if err != nil {
			break
		}
		rt_user.ProcessMessage(message)
	}
}

func (rt_user *RealTimeUser) ProcessMessage(message []byte) {
	msg := &RealTimeMessage{
		Content: string(message[:]),
		Sender:  rt_user.User.Username,
		Group:   rt_user.GroupId,
		Type:    "Text",
		Time:    time.Now(),
	}
	if len(msg.Content) == 0 {
		return
	}
	now := time.Now()
	rt_user.LastActivity = now
	room := ActiveGroups[rt_user.GroupId]
	room.Announce(msg, rt_user)
}

func PrepareMessage(content interface{}) []byte {
	resp, err := json.Marshal(content)
	if err == nil {
		return resp
	}
	return []byte("")
}

func NewGroupClient(db *mgo.Session) *GroupDataClient {
	g := new(GroupDataClient)
	g.db = db
	g.collection = "groups"
	g.dbName = "superfish"
	return g
}

func (gr *GroupDataClient) IsUserInGroup(id bson.ObjectId, u *User) (bool, *Group) {
	group, err := gr.FindGroupById(id)
	if err != nil {
		log.Println(err)
		return false, nil
	}
	if IsItemInArray(&group.Members, u.Username) {
		return true, group
	}
	return false, nil
}

func (g *GroupDataClient) FindGroupById(id bson.ObjectId) (*Group, error) {
	group := new(Group)
	groupID, err := ParseIdFromString(id.Hex())
	if err != nil {
		return nil, err
	}
	c := g.db.DB(g.dbName).C(g.collection)
	err2 := c.FindId(groupID).One(group)
	return group, err2
}

func (g *GroupDataClient) FormatGroupContent(data *GroupPost, curr_user *User) (*Group, error) {
	group := new(Group)
	users, err := g.CreateMembersArray(data.Members)
	exists := IsItemInArray(users, curr_user.Username)
	group.Name = data.Name
	group.Id = bson.NewObjectId()
	group.Messages = g.NewMessagesList()
	if exists {
		group.Members = *users
	} else {
		group.Members = append(*users, *curr_user)
	}
	return group, err
}

func (g *GroupDataClient) NewMessagesList() []Message {
	msg := make([]Message, 0)
	return msg
}

func (g *GroupDataClient) CreateMembersArray(members []string) (*[]User, error) {
	users := make([]User, 1)
	c := g.db.DB(g.dbName).C("users")
	query := bson.M{"username": bson.M{"$in": members}}
	err := c.Find(query).All(&users)
	return &users, err
}

func (g *GroupDataClient) NewGroup(group *Group) error {
	c := g.db.DB(g.dbName).C(g.collection)
	err := c.Insert(group)
	return err
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

func (u *UserDataClient) GetById(id string) (*User, error) {
	user := new(User)
	userID, err := ParseIdFromString(id)
	if err != nil {
		return nil, err
	}
	c := u.db.DB(u.dbName).C(u.collection)
	err2 := c.FindId(userID).One(user)
	return user, err2
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
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(group_post); err != nil {
		ServerError(w, err)
		return
	}
	if !ValidateName(group_post.Name) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(ErrGroupNameInvalid.Error()))
		return
	}
	db := GetMongoSession(r)
	g := NewGroupClient(db)
	group, err := g.FormatGroupContent(group_post, curr_user)
	if err := g.NewGroup(group); err != nil {
		ServerError(w, err)
	}
	gj, _ := json.Marshal(group)
	w.WriteHeader(http.StatusOK)
	w.Write(gj)
}

func currentUser(w http.ResponseWriter, r *http.Request) (*User, error) {
	if !existsToken(r) {
		return nil, ErrUnauthorizedAccess
	}
	db := GetMongoSession(r)
	us := NewUserData(db)
	userId, token := parseToken(r)
	user, err := us.GetById(userId)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(user.Token, "_")
	if parts[1] == token {
		return user, nil
	}
	return nil, ErrUnauthorizedAccess
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

func WebsocketHandler(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		ServerError(w, err)
		return
	}
	groupId, err := GetIdFromPath(r, "group_id")
	if err != nil {
		ServerError(w, err)
		return
	}
	db := GetMongoSession(r)
	gr := NewGroupClient(db)
	exists, group := gr.IsUserInGroup(groupId, curr_user)
	if !exists {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	ws, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		ServerError(w, err)
		return
	}

	rt_group, exists := ActiveGroups[groupId.Hex()]
	if !exists {
		rt_group = InitializeRTGroup(group, gr)
	}

	rt_user := &RealTimeUser{
		User:         curr_user,
		Ws:           ws,
		SendQueue:    make(chan []byte),
		GroupId:      group.Id.Hex(),
		LastActivity: time.Now(),
	}

	rt_group.Register <- rt_user

	go rt_user.Talk()
	rt_user.Listen()
}

func InitializeRTGroup(g *Group, gr *GroupDataClient) *RealTimeGroup {
	rt_group := &RealTimeGroup{
		Group:      g,
		DataClient: gr,
		Broadcast:  make(chan []byte),
		Register:   make(chan *RealTimeUser),
		Unregister: make(chan *RealTimeUser),
		Users:      make(map[*RealTimeUser]bool),
		Stop:       make(chan int),
	}

	go rt_group.Run()
	ActiveGroups[rt_group.Group.Id.Hex()] = rt_group

	return rt_group
}

func IsItemInArray(members *[]User, name string) bool {
	exists := false
	for _, v := range *members {
		if v.Username == name {
			exists = true
		}
	}
	return exists
}

func GetIdFromPath(r *http.Request, param string) (bson.ObjectId, error) {
	err := errors.New("Could not parse group id")
	urlParam := mux.Vars(r)["group_id"]
	if urlParam == "" || !bson.IsObjectIdHex(urlParam) {
		return "", err
	}
	roomId := bson.ObjectIdHex(urlParam)
	return roomId, nil
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

func ParseIdFromString(stringID string) (bson.ObjectId, error) {
	if stringID == "" || !bson.IsObjectIdHex(stringID) {
		return "", errors.New("id is malformed")
	}
	return bson.ObjectIdHex(stringID), nil
}

//ServerError handles server errors writing a response to the client and logging the error.
func ServerError(w http.ResponseWriter, err error) {
	log.Println(err)
	w.Header().Set("success", "false")
	w.Header().Set("code", "50")
	w.WriteHeader(http.StatusInternalServerError)
}

func existsToken(rq *http.Request) bool {
	token := rq.URL.Query().Get("token")
	if token != "" {
		return true
	}
	return false
}

func parseToken(rq *http.Request) (u, t string) {
	token := rq.URL.Query().Get("token")
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
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	return hex.EncodeToString(token), nil
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

	Upgrader = new(websocket.Upgrader)
	Upgrader.ReadBufferSize = 2056
	Upgrader.WriteBufferSize = 2056
	Upgrader.CheckOrigin = func(r *http.Request) bool {
		return true
	}
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
	router.HandleFunc("/ws/{group_id}", WebsocketHandler).Methods("GET")
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
