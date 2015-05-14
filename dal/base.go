package dal

import "gopkg.in/mgo.v2"

type Base struct {
	db         *mgo.Session
	collection string
	hasID      bool
}
