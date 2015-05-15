package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/context"
	"github.com/ziyadparekh/superfish/dal"
	"github.com/ziyadparekh/superfish/libhttp"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func PostRoomCreate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !isUserLoggedIn(w, r) {
		notAllowed := errors.New("You need to be logged in to perform this action")
		libhttp.HandleErrorJson(w, notAllowed)
		return
	}

	rs := dal.RoomModel{}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rs); err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	i := bson.NewObjectId()
	rs.Id = i

	currentUser := getCurrentUser(w, r)
	db := context.Get(r, "db").(*mgo.Session)

	err := dal.NewRoom(db).CreateRoom(currentUser, &rs)
	if err != nil {
		logrus.Error(err.Error())
		libhttp.HandleErrorJson(w, err)
		return
	}

	rj, err := json.Marshal(&rs)
	if err != nil {
		logrus.Error(err.Error())
		libhttp.HandleErrorJson(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(rj)
	return
}

func UpdateRoomMembers(w http.ResponseWriter, r *http.Request) {
	db := context.Get(r, "db").(*mgo.Session)
	r_db := dal.NewRoom(db)

	rs := &dal.RoomModel{}
	rid, rs, err := genericValidation(w, r)

	if db_err := r_db.UpdateRoomMembersById(rid, rs.Members); db_err != nil {
		libhttp.HandleErrorJson(w, db_err)
		return
	}

	room, err := r_db.GetRoomById(rid)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}
	rj, _ := json.Marshal(room)
	w.WriteHeader(http.StatusOK)
	w.Write(rj)
	return
}

func UpdateRoomName(w http.ResponseWriter, r *http.Request) {
	db := context.Get(r, "db").(*mgo.Session)
	r_db := dal.NewRoom(db)

	rs := &dal.RoomModel{}
	rid, rs, err := genericValidation(w, r)

	if db_err := r_db.UpdateRoomNameById(rid, rs.Name); db_err != nil {
		libhttp.HandleErrorJson(w, db_err)
		return
	}

	room, err := r_db.GetRoomById(rid)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}
	rj, _ := json.Marshal(room)
	w.WriteHeader(http.StatusOK)
	w.Write(rj)
	return
}

func genericValidation(w http.ResponseWriter, r *http.Request) (rid bson.ObjectId, rs *dal.RoomModel, err error) {
	w.Header().Set("Content-Type", "application/json")

	if !isUserLoggedIn(w, r) {
		notAllowed := errors.New("You need to be logged in to perform this action")
		return "", nil, notAllowed
	}

	rid, err = getIdFromPath(w, r)
	if err != nil {
		return "", nil, err
	}

	currentUser := getCurrentUser(w, r)
	db := context.Get(r, "db").(*mgo.Session)

	r_db := dal.NewRoom(db)
	room, err := r_db.GetRoomById(rid)
	if err != nil {
		return "", nil, err
	}

	if !isUserInRoom(currentUser.Username, room.Members) {
		notMember := errors.New("You need to be a member of the group to make updates")
		return "", nil, notMember
	}

	rs = &dal.RoomModel{}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(rs); err != nil {
		return "", nil, err
	}

	return rid, rs, nil
}

func isUserInRoom(user string, members []string) bool {
	for _, u := range members {
		if user == u {
			return true
		}
	}
	return false
}
