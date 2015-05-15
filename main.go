package main

import (
	"encoding/gob"
	"net/http"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/carbocation/interpose"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/ziyadparekh/superfish/dal"
	"github.com/ziyadparekh/superfish/handlers"
	"github.com/ziyadparekh/superfish/libenv"
	"github.com/ziyadparekh/superfish/middlewares"
	"gopkg.in/mgo.v2"
	"gopkg.in/tylerb/graceful.v1"
)

func init() {
	gob.Register(&dal.UserModel{})
}

type Application struct {
	dsn         string
	db          *mgo.Session
	cookieStore *sessions.CookieStore
}

func NewApplication() (*Application, error) {
	dsn := "mongodb://localhost"
	db, err := mgo.Dial(dsn)
	if err != nil {
		return nil, err
	}

	cookieStoreSecret := "supersecretcookie"

	rm := &Application{}
	rm.dsn = dsn
	rm.db = db
	rm.cookieStore = sessions.NewCookieStore([]byte(cookieStoreSecret))
	return rm, err
}

func (rm *Application) middlewareStruct() (*interpose.Middleware, error) {
	middle := interpose.New()
	middle.Use(middlewares.SetDB(rm.db))
	middle.Use(middlewares.SetCookieStore(rm.cookieStore))

	middle.UseHandler(rm.routeMux())

	return middle, nil
}

func (rm *Application) routeMux() *mux.Router {
	MustLogin := middlewares.MustLogin

	router := mux.NewRouter()
	router.Handle("/", MustLogin(http.HandlerFunc(handlers.GetLogin))).Methods("GET")

	router.HandleFunc("/signup", handlers.PostSignup).Methods("POST")
	router.HandleFunc("/users/{id}", handlers.UpdateUser).Methods("PUT")
	router.HandleFunc("/users/rooms", handlers.GetUsersRooms).Methods("GET")

	router.HandleFunc("/room", handlers.PostRoomCreate).Methods("POST")
	router.HandleFunc("/room/{id}", handlers.GetRoomById).Methods("GET")
	router.HandleFunc("/room/{id}/name", handlers.UpdateRoomName).Methods("PUT")
	router.HandleFunc("/room/{id}/members", handlers.UpdateRoomMembers).Methods("PUT")

	return router
}

func main() {
	app, err := NewApplication()
	if err != nil {
		logrus.Error(err.Error())
	}

	middle, err := app.middlewareStruct()
	if err != nil {
		logrus.Error(err.Error())
	}

	serverAddress := libenv.EnvWithDefault("HTTP_ADDR", ":8888")
	certFile := libenv.EnvWithDefault("HTTP_CERT_FILE", "")
	keyFile := libenv.EnvWithDefault("HTTP_KEY_FILE", "")
	drainIntervalString := libenv.EnvWithDefault("HTTP_DRAIN_INTERVAL", "1s")

	drainInterval, err := time.ParseDuration(drainIntervalString)
	if err != nil {
		logrus.Error(err.Error())
	}

	srv := &graceful.Server{
		Timeout: drainInterval,
		Server:  &http.Server{Addr: serverAddress, Handler: middle},
	}

	logrus.Infoln("Running HTTP server on " + serverAddress)
	if certFile != "" && keyFile != "" {
		srv.ListenAndServeTLS(certFile, keyFile)
	} else {
		srv.ListenAndServe()
	}
}
