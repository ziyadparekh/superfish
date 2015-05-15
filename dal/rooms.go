package dal

import (
	"time"

	"github.com/Sirupsen/logrus"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

type Room struct {
	Base
}

type RoomModel struct {
	Id      bson.ObjectId `json:"id,omitempty" bson:"_id,omitempty"`
	Name    string        `json:"name" bson:"name"`
	Members []string      `json:"members" bson:"members"`
	Created time.Time     `json:"created" bson:"created"`
	Updated time.Time     `json:"updated" bson:"updated"`
}

func NewRoom(db *mgo.Session) *Room {
	room := &Room{}
	room.collection = "rooms"
	room.db = db
	room.hasID = true

	return room
}

func (r *Room) CreateRoom(cu *UserModel, rm *RoomModel) error {
	// Add current user to list of members
	rm.Members = append(rm.Members, cu.Username)
	// Get instance of rooms collection
	c := r.db.DB("superfish").C(r.collection)
	// Insert room into collection
	err := c.Insert(rm)
	if err != nil {
		logrus.Error(err.Error())
		return err
	}

	return nil
}

func (r *Room) GetRoomById(roomId bson.ObjectId) (*RoomModel, error) {
	room := &RoomModel{}
	c := r.db.DB("superfish").C(r.collection)
	err := c.FindId(roomId).One(room)

	return room, err

}

func (r *Room) UpdateRoomMembersById(roomId bson.ObjectId, members []string) error {
	c := r.db.DB("superfish").C(r.collection)
	colQuery := bson.M{"_id": roomId}
	change := bson.M{"$set": bson.M{"members": members}}
	err := c.Update(colQuery, change)
	if err != nil {
		return err
	}
	return nil
}

func (r *Room) UpdateRoomNameById(roomId bson.ObjectId, name string) error {

	c := r.db.DB("superfish").C(r.collection)
	colQuery := bson.M{"_id": roomId}
	change := bson.M{"$set": bson.M{"name": name}}
	err := c.Update(colQuery, change)
	if err != nil {
		return err
	}
	return nil
}
