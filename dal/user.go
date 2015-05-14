package dal

import (
	"errors"

	"github.com/Sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

type User struct {
	Base
}

type UserModel struct {
	Username string        `json:"username" bson:"username"`
	Number   string        `json:"number" bson:"number"`
	Password string        `json:"-" bson:"password"`
	Id       bson.ObjectId `json:"id" bson:"_id"`
}

func NewUser(db *mgo.Session) *User {
	user := &User{}
	user.db = db
	user.collection = "users"
	user.hasID = true

	return user
}

func (u *User) Signup(number, username, password string) error {
	if number == "" {
		return errors.New("Number cannot be blank")
	}
	if password == "" {
		return errors.New("Password cannot be blank")
	}
	if username == "" {
		return errors.New("Username cannot be blank")
	}

	c := u.db.DB("superfish").C(u.collection)

	_, err := u.GetByUsername(username)
	if err == nil {
		return errors.New("Username is taken")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 5)
	if err != nil {
		logrus.Error(err.Error())
		return err
	}
	data := make(map[string]interface{})
	data["username"] = username
	data["number"] = number
	data["password"] = hashedPassword

	db_err := c.Insert(&data)

	if db_err != nil {
		logrus.Error(db_err.Error())
		return db_err
	}

	return nil
}

func (u *User) GetByUsername(username string) (*UserModel, error) {
	user := &UserModel{}
	c := u.db.DB("superfish").C(u.collection)
	err := c.Find(bson.M{"username": username}).One(user)

	return user, err
}

func (u *User) GetById(userId bson.ObjectId) (*UserModel, error) {
	user := &UserModel{}
	c := u.db.DB("superfish").C(u.collection)
	err := c.Find(bson.M{"_id": userId}).One(user)

	return user, err
}

func (u *User) GetUserByUsernameAndPassword(username, password string) (*UserModel, error) {
	user, err := u.GetByUsername(username)
	if err != nil {
		return nil, err
	}

	p_err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if p_err != nil {
		return nil, p_err
	}

	return user, nil
}

func (u *User) UpdateNumberById(userId bson.ObjectId, number string) error {
	if number == "" {
		return errors.New("Number cannot be blank")
	}

	c := u.db.DB("superfish").C(u.collection)
	colQuery := bson.M{"_id": userId}
	change := bson.M{"$set": bson.M{"number": number}}
	db_err := c.Update(colQuery, change)

	if db_err != nil {
		return db_err
	}

	return nil
}

func (u *User) UpdatePasswordById(userId bson.ObjectId, password string) error {
	if password == "" {
		return errors.New("Password cannot be blank")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 5)
	if err != nil {
		return err
	}

	c := u.db.DB("superfish").C(u.collection)
	colQuery := bson.M{"_id": userId}
	change := bson.M{"$set": bson.M{"password": hashedPassword}}
	db_err := c.Update(colQuery, change)

	if db_err != nil {
		return db_err
	}

	return nil
}
