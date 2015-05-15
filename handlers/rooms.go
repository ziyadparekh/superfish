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
	// This is all copy pasted and should be shared but i am lazy as fuck
	w.Header().Set("Content-Type", "application/json")

	if !isUserLoggedIn(w, r) {
		notAllowed := errors.New("You need to be logged in to perform this action")
		libhttp.HandleErrorJson(w, notAllowed)
		return
	}

	rid, err := getIdFromPath(w, r)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	currentUser := getCurrentUser(w, r)
	db := context.Get(r, "db").(*mgo.Session)

	r_db := dal.NewRoom(db)
	room, err := r_db.GetRoomById(rid)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	if !isUserInRoom(currentUser.Username, room.Members) {
		notMember := errors.New("You need to be a member of the group to make updates")
		libhttp.HandleErrorJson(w, notMember)
		return
	}

	rs := dal.RoomModel{}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rs); err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	if db_err := r_db.UpdateRoomMembersById(rid, rs.Members); db_err != nil {
		libhttp.HandleErrorJson(w, db_err)
		return
	}

	room, err = r_db.GetRoomById(rid)
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
	w.Header().Set("Content-Type", "application/json")

	if !isUserLoggedIn(w, r) {
		notAllowed := errors.New("You need to be logged in to perform this action")
		libhttp.HandleErrorJson(w, notAllowed)
		return
	}

	rid, err := getIdFromPath(w, r)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	currentUser := getCurrentUser(w, r)
	db := context.Get(r, "db").(*mgo.Session)

	r_db := dal.NewRoom(db)
	room, err := r_db.GetRoomById(rid)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	if !isUserInRoom(currentUser.Username, room.Members) {
		notMember := errors.New("You need to be a member of the group to make updates")
		libhttp.HandleErrorJson(w, notMember)
		return
	}

	rs := dal.RoomModel{}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rs); err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}

	if db_err := r_db.UpdateRoomNameById(rid, rs.Name); db_err != nil {
		libhttp.HandleErrorJson(w, db_err)
		return
	}

	room, err = r_db.GetRoomById(rid)
	if err != nil {
		libhttp.HandleErrorJson(w, err)
		return
	}
	rj, _ := json.Marshal(room)
	w.WriteHeader(http.StatusOK)
	w.Write(rj)
	return
}

func isUserInRoom(user string, members []string) bool {
	for _, u := range members {
		if user == u {
			return true
		}
	}
	return false
}
