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
	"strconv"
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

var ChatBotID = "557f9de43c5d63a527000001"
var ChatBotWs *websocket.Conn
var ChatBot *RealTimeUser

type ApiResponse struct {
	Status   int         `json:"status"`
	Response interface{} `json:"response"`
}

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
	Id       bson.ObjectId     `json:"groupId" bson:"_id"`
	Name     string            `json:"name" bson:"name"`
	Admin    string            `json:"admin" bson:"admin"`
	Activity time.Time         `json:"activity" bson:"activity"`
	Members  []User            `json:"members" bson:"members"`
	Messages []RealTimeMessage `json:"messages" bson:"messages"`
}

type RealTimeGroup struct {
	Group      *Group                 `json:"group"`
	DataClient *GroupDataClient       `json:"-"`
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

type MessagePost struct {
	GroupId string `json:"groupId"`
	Content string `json:"content"`
}

type GroupPost struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
	Token   string   `json:"token"`
}

type ContactsPost struct {
	Contacts []string `json:"contacts"`
	Token    string   `json:"token"`
}

type Contacts struct {
	UserId   bson.ObjectId `json:"user_id" bson:"user_id"`
	Contacts []User        `json:"contacts" bson:"contacts"`
}

type Pagination struct {
	Limit  string `json:"limit"`
	Offset string `json:"offset"`
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

type ContactsDataClient struct {
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
					select {
					case user.SendQueue <- PrepareMessage(msg):
					default:
						close(user.SendQueue)
						delete(rt_group.Users, user)
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

func (rt_group *RealTimeGroup) Announce(message *RealTimeMessage) {
	if len(rt_group.Users) > 0 {
		rt_group.Broadcast <- PrepareMessage(message)
		if err := rt_group.DataClient.AddMessageToDatabase(message); err != nil {
			log.Println(err)
		}
		if err := rt_group.DataClient.UpdateGroupActivity(rt_group.Group.Id.Hex()); err != nil {
			log.Println(err)
		}
	}
}

func (g *GroupDataClient) AddMessageToDatabase(message *RealTimeMessage) error {
	C := g.db.DB(g.dbName).C("messages")
	err := C.Insert(message)
	return err
}

func (g *GroupDataClient) UpdateGroupActivity(id string) error {
	groupId, err := ParseIdFromString(id)
	if err != nil {
		return err
	}
	c := g.db.DB(g.dbName).C(g.collection)
	query := bson.M{"$set": bson.M{"activity": time.Now()}}
	if err := c.Update(bson.M{"_id": groupId}, query); err != nil {
		return err
	}
	return nil
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
	msg_post := new(MessagePost)
	if err := json.Unmarshal(message, msg_post); err != nil {
		log.Println(err)
		return
	}
	msg := &RealTimeMessage{
		Content: msg_post.Content,
		Sender:  rt_user.User.Username,
		Group:   msg_post.GroupId, //parse room id from message
		Type:    "Text",
		Time:    time.Now(),
	}
	if len(msg.Content) == 0 {
		return
	}
	now := time.Now()
	rt_user.LastActivity = now
	room := ActiveGroups[msg.Group]
	room.Announce(msg)
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

func (g *GroupDataClient) UpdateGroupName(id bson.ObjectId, name string) error {
	c := g.db.DB(g.dbName).C(g.collection)
	colQuery := bson.M{"_id": id}
	change := bson.M{"$set": bson.M{"name": name}}
	err := c.Update(colQuery, change)
	return err
}

func (g *GroupDataClient) UpdateGroupMembers(id bson.ObjectId, members []string, remove string) error {
	users, err := g.CreateMembersArray(members)
	if err != nil {
		return err
	}
	c := g.db.DB(g.dbName).C(g.collection)
	colQuery := bson.M{"_id": id}
	change := bson.M{"$addToSet": bson.M{"members": bson.M{"$each": users}}}
	if remove == "true" {
		change = bson.M{"$pullAll": bson.M{"members": users}}
	}
	err2 := c.Update(colQuery, change)
	return err2
}

func (g *GroupDataClient) IsUserInGroup(id bson.ObjectId, u *User, full bool) (bool, *Group) {
	group, err := g.FindGroupById(id, full)
	if err != nil {
		log.Println(err)
		return false, nil
	}
	if IsItemInArray(&group.Members, u.Username) {
		return true, group
	}
	return false, nil
}

func (g *GroupDataClient) IsUserGroupAdmin(id bson.ObjectId, u *User, full bool) (bool, *Group) {
	group, err := g.FindGroupById(id, full)
	if err != nil {
		log.Println(err)
		return false, nil
	}
	if group.Admin == u.Username {
		return true, group
	}
	return false, nil
}

func (g *GroupDataClient) GetGroupsForUser(user *User) (*[]Group, error) {
	username := [1]*User{user}
	groups := make([]Group, 0)
	query := bson.M{"members": bson.M{"$all": username}}
	c := g.db.DB(g.dbName).C(g.collection)
	err := c.Find(query).Sort("-activity").All(&groups)

	for i, gr := range groups {
		msg, err := g.GetLastMessageForGroup(gr.Id)
		if err != mgo.ErrNotFound {
			groups[i].Messages = append(groups[i].Messages, *msg)
		}
	}
	return &groups, err
}

func (g *GroupDataClient) GetLastMessageForGroup(gid bson.ObjectId) (*RealTimeMessage, error) {
	message := new(RealTimeMessage)
	c := g.db.DB(g.dbName).C("messages")
	err := c.Find(bson.M{"group": gid.Hex()}).Sort("-time").One(message)
	return message, err
}

func (g *GroupDataClient) FindGroupById(id bson.ObjectId, full bool) (*Group, error) {
	group := new(Group)
	groupID, err := ParseIdFromString(id.Hex())
	if err != nil {
		return nil, err
	}
	c := g.db.DB(g.dbName).C(g.collection)
	err2 := c.FindId(groupID).One(group)
	if full {
		messages, err := g.GetAllMessagesForGroup(groupID)
		if err != mgo.ErrNotFound {
			group.Messages = *messages
		}
	}
	return group, err2
}

func (g *GroupDataClient) GetPaginatedMessagesForGroup(id bson.ObjectId, pag *Pagination) (*[]RealTimeMessage, error) {
	l, _ := strconv.Atoi(pag.Limit)
	o, _ := strconv.Atoi(pag.Offset)
	messages := make([]RealTimeMessage, 0)
	c := g.db.DB(g.dbName).C("messages")
	err := c.Find(bson.M{"group": id.Hex()}).Limit(l).Skip(o).Sort("-time").All(&messages)
	return &messages, err
}

func (g *GroupDataClient) GetAllMessagesForGroup(gid bson.ObjectId) (*[]RealTimeMessage, error) {
	messages := make([]RealTimeMessage, 0)
	c := g.db.DB(g.dbName).C("messages")
	err := c.Find(bson.M{"group": gid.Hex()}).Limit(20).Skip(0).Sort("-time").All(&messages)
	return &messages, err
}

func (g *GroupDataClient) FormatGroupContent(data *GroupPost, curr_user *User) (*Group, error) {
	group := new(Group)
	users, err := g.CreateMembersArray(data.Members)
	exists := IsItemInArray(users, curr_user.Username)
	group.Name = data.Name
	group.Activity = time.Now()
	group.Id = bson.NewObjectId()
	group.Messages = g.NewMessagesList()
	group.Admin = curr_user.Username
	if exists {
		group.Members = *users
	} else {
		group.Members = append(*users, *curr_user)
	}
	return group, err
}

func (g *GroupDataClient) NewMessagesList() []RealTimeMessage {
	msg := make([]RealTimeMessage, 0)
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

func NewContactsClient(db *mgo.Session) *ContactsDataClient {
	c := new(ContactsDataClient)
	c.db = db
	c.collection = "contacts"
	c.dbName = "superfish"
	return c
}

func (c *ContactsDataClient) FilterUsersContacts(contacts []string, curr_user *User) (*[]User, error) {
	users := make([]User, 1)
	uc := c.db.DB(c.dbName).C("users")
	cc := c.db.DB(c.dbName).C(c.collection)

	query1 := bson.M{"number": bson.M{"$in": contacts}}
	uc.Find(query1).All(&users)

	colQuery := bson.M{"user_id": curr_user.Id}
	query2 := bson.M{"$addToSet": bson.M{"contacts": bson.M{"$each": users}}}
	if err2 := cc.Update(colQuery, query2); err2 != mgo.ErrNotFound {
		return &users, err2
	}
	cont := new(Contacts)
	cont.UserId = curr_user.Id
	cont.Contacts = users
	err3 := cc.Insert(cont)

	return &users, err3
}

func (c *ContactsDataClient) FetchUserContacts(curr_user *User) (*Contacts, error) {
	contacts := new(Contacts)
	cc := c.db.DB(c.dbName).C(c.collection)
	err := cc.Find(bson.M{"user_id": curr_user.Id}).One(contacts)
	return contacts, err
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
	//TODO:: also validate phone numbers
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

func UpdateGroupMembers(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	group_id, err := GetIdFromPath(r, "group_id")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	db := GetMongoSession(r)
	gr := NewGroupClient(db)
	if exists, _ := gr.IsUserGroupAdmin(group_id, curr_user, true); !exists {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	remove := r.URL.Query().Get("remove")
	gp := new(GroupPost)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(gp); err != nil {
		ServerError(w, err)
		return
	}
	if err := gr.UpdateGroupMembers(group_id, gp.Members, remove); err != nil {
		ServerError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func UpdateGroupName(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	group_id, err := GetIdFromPath(r, "group_id")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	db := GetMongoSession(r)
	gr := NewGroupClient(db)
	if exists, _ := gr.IsUserGroupAdmin(group_id, curr_user, true); !exists {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	gp := new(GroupPost)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(gp); err != nil {
		ServerError(w, err)
		return
	}
	if err := gr.UpdateGroupName(group_id, gp.Name); err != nil {
		ServerError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func GetGroupMessages(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	group_id, err := GetIdFromPath(r, "group_id")
	if err != nil {
		ServerError(w, err)
		return
	}
	db := GetMongoSession(r)
	gr := NewGroupClient(db)
	if exists, _ := gr.IsUserInGroup(group_id, curr_user, true); !exists {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	pagination := &Pagination{
		Limit:  r.URL.Query().Get("limit"),
		Offset: r.URL.Query().Get("offset"),
	}
	messages, err := gr.GetPaginatedMessagesForGroup(group_id, pagination)
	switch {
	case err == mgo.ErrCursor:
		w.WriteHeader(http.StatusBadRequest)
	case err == mgo.ErrNotFound:
		w.WriteHeader(http.StatusNotFound)
	case err != nil:
		ServerError(w, err)
	default:
		WriteResponse(w, messages)
	}
}

func GetGroups(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	db := GetMongoSession(r)
	gr := NewGroupClient(db)
	groups, err := gr.GetGroupsForUser(curr_user)
	switch {
	case err == mgo.ErrNotFound:
		w.WriteHeader(http.StatusNotFound)
	case err != nil:
		ServerError(w, err)
	default:
		WriteResponse(w, groups)
	}
}

func WriteResponse(w http.ResponseWriter, content interface{}) {
	response := new(ApiResponse)
	response.Response = content
	response.Status = http.StatusOK
	rj, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(rj)
}

func GetSingleGroup(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	group_id, err := GetIdFromPath(r, "group_id")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	db := GetMongoSession(r)
	gr := NewGroupClient(db)
	exists, group := gr.IsUserInGroup(group_id, curr_user, true)
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	gj, _ := json.Marshal(group)
	w.WriteHeader(http.StatusOK)
	w.Write(gj)
}

func FetchContacts(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	db := GetMongoSession(r)
	cc := NewContactsClient(db)
	contacts, err := cc.FetchUserContacts(curr_user)
	switch {
	case err == mgo.ErrNotFound:
		w.WriteHeader(http.StatusNotFound)
	case err != nil:
		ServerError(w, err)
	default:
		WriteResponse(w, contacts)
	}
}

func CreateGroup(w http.ResponseWriter, r *http.Request) {
	group_post := new(GroupPost)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(group_post); err != nil {
		ServerError(w, err)
		return
	}
	curr_user, err := currentUserByPost(r, group_post.Token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
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

func FilterContacts(w http.ResponseWriter, r *http.Request) {
	contacts_post := new(ContactsPost)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(contacts_post); err != nil {
		ServerError(w, err)
		return
	}
	curr_user, err := currentUserByPost(r, contacts_post.Token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	db := GetMongoSession(r)
	c := NewContactsClient(db)
	users, err := c.FilterUsersContacts(contacts_post.Contacts, curr_user)
	switch {
	case err == mgo.ErrNotFound:
		w.WriteHeader(http.StatusNotFound)
	case err != nil:
		ServerError(w, err)
	default:
		WriteResponse(w, users)
	}
}

func currentUserByPost(r *http.Request, token string) (*User, error) {
	if token == "" {
		return nil, ErrUnauthorizedAccess
	}
	db := GetMongoSession(r)
	us := NewUserData(db)
	parts := strings.Split(token, "_")
	user, err := us.GetById(parts[0])
	if err != nil {
		return nil, err
	}
	userToken := strings.Split(user.Token, "_")
	if parts[1] == userToken[1] {
		return user, nil
	}
	return nil, ErrUnauthorizedAccess
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
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
	case err != nil:
		ServerError(w, err)
	default:
		uj, _ := json.Marshal(user)
		w.WriteHeader(http.StatusOK)
		w.Write(uj)
	}
}

func ChatbotHandler(w http.ResponseWriter, r *http.Request) {
	userId, err := GetIdFromPath(r, "user_id")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	db := GetMongoSession(r)
	u := NewUserData(db)
	user, err := u.GetById(userId.Hex())
	switch {
	case err == mgo.ErrNotFound:
		w.WriteHeader(http.StatusNotFound)
	case err != nil:
		ServerError(w, err)
	case !IsUserChatBot(user):
		w.WriteHeader(http.StatusUnauthorized)
	default:
		ChatBotWs, err = Upgrader.Upgrade(w, r, nil)
		if err != nil {
			ServerError(w, err)
		}
		ChatBot = &RealTimeUser{
			User:         user,
			Ws:           ChatBotWs,
			SendQueue:    make(chan []byte),
			LastActivity: time.Now(),
		}
		go ChatBot.Talk()
		ChatBot.Listen()
	}
}

func IsUserChatBot(u *User) bool {
	return u.Id.Hex() == ChatBotID
}

func WebsocketHandler(w http.ResponseWriter, r *http.Request) {
	curr_user, err := currentUser(w, r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	groupId, err := GetIdFromPath(r, "group_id")
	if err != nil {
		ServerError(w, err)
		return
	}
	db := GetMongoSession(r)
	gr := NewGroupClient(db)
	exists, group := gr.IsUserInGroup(groupId, curr_user, false)
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
	if ChatBot != nil {
		rt_group.Register <- RegisterChatBotInRoom(group.Id)
	}
	go rt_user.Talk()
	rt_user.Listen()
}

func RegisterChatBotInRoom(groupId bson.ObjectId) *RealTimeUser {
	ChatBot.GroupId = groupId.Hex()
	return ChatBot
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
	urlParam := mux.Vars(r)[param]
	if urlParam == "" || !bson.IsObjectIdHex(urlParam) {
		return "", err
	}
	roomId := bson.ObjectIdHex(urlParam)
	return roomId, nil
}

func ValidateUser(u *User) error {
	if !ValidateName(u.Username) || len(u.Username) == 0 {
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
	router.HandleFunc("/contacts", FilterContacts).Methods("POST")
	router.HandleFunc("/contacts", FetchContacts).Methods("GET")
	router.HandleFunc("/group", CreateGroup).Methods("POST")
	router.HandleFunc("/group/{group_id}", GetSingleGroup).Methods("GET")
	router.HandleFunc("/group/{group_id}/messages", GetGroupMessages).Methods("GET")
	router.HandleFunc("/group/{group_id}/name", UpdateGroupName).Methods("PUT")
	router.HandleFunc("/group/{group_id}/members", UpdateGroupMembers).Methods("PUT")
	router.HandleFunc("/groups", GetGroups).Methods("GET")
	router.HandleFunc("/ws/chatbot/{user_id}", ChatbotHandler).Methods("GET")
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
