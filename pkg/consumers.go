package pkg

import (
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type (
	//State describes the interface for maintaining state of instances for a consumer
	State interface {
		Changed() bool
		Created() bool
		Notify(string)
		Teardown() error
	}
	//ParserLoader describes the interface for maintaining the data in a consumer
	ParserLoader interface {
		Load(db *AgentDB) error
		Save(db *AgentDB) error
		Parse() (State, error)
		Register() []string
	}
	//BaseConsumers is a type to describe multiple BaseConsumers
	BaseConsumers []*BaseConsumer

	//BaseConsumer is a struct that contains the base objects needed to make a consumer
	BaseConsumer struct {
		*AgentDB
		ParserLoader
		sync.RWMutex
	}
)

//Init function for populating a base consumer
func (bc *BaseConsumer) Init() error {
	if err := bc.Load(bc.AgentDB); err != nil {
		return err
	}
	state, err := bc.Parse()
	if err != nil {
		return err
	}
	if !state.Created() && state.Changed() {
		state.Notify("baseInit")
	}
	if err := bc.Save(bc.AgentDB); err != nil {
		return err
	}
	if err := state.Teardown(); err == nil || err == ErrReload {
		return nil
	}
	return err
}

//Consume consumes an event
func (bc *BaseConsumer) Consume(e Event) error {
	bc.Lock()
	defer bc.Unlock()
	state, err := bc.Parse()
	if err != nil {
		return err
	}
	if !state.Changed() {
		return state.Teardown()
	}
	state.Notify(e.Com)
	if err := bc.Save(bc.AgentDB); err != nil {
		return err
	}
	return state.Teardown()
}

//Register method maps files to consumers.
func (bc *BaseConsumer) Register() *sync.Map {
	consumers := &sync.Map{}
	for _, file := range bc.ParserLoader.Register() {
		consumers.Store(file, bc)
	}
	return consumers
}

//Consumers returns a slice of consumers.
func (bc BaseConsumers) Consumers() (consumers []Consumer) {
	for _, consumer := range bc {
		consumers = append(consumers, consumer)
	}
	return consumers
}

/* --------------------------------- USERS --------------------------------- */

type (
	usersState struct {
		users    Users
		includes []string
	}
	//UsersState struct keeps track of state changes based on UserListener struct and methods
	UsersState struct {
		*UsersListener
		current, next *usersState
	}
)

//Parse calls parse(), and update new UserState
func (us *UsersState) Parse() (State, error) {
	users, includes, err := us.parse()
	if err != nil {
		return nil, err
	}
	us.next = &usersState{includes: includes, users: users}
	return us, nil
}

//Changed checks if the new UserState instance is different from old UserState instance
func (us *UsersState) Changed() bool {
	add, del := userDiff(us.current.users, us.next.users)
	return len(add) != 0 || len(del) != 0
}

//Created checks if the current UserState has been created
func (us *UsersState) Created() bool { return len(us.current.users) == 0 }

//Notify is the method to notify of a change in state
func (us *UsersState) Notify(cmd string) {
	add, del := userDiff(us.current.users, us.next.users)
	us.Warn().
		Array("users", LogUsers(us.next.users)).
		Array("add", LogUsers(add)).
		Array("del", LogUsers(del)).
		Str("processName", cmd).
		Msg("Users Modified")
}

func (us *UsersState) reload() error {
	if ArrayEqual(us.current.includes, us.next.includes) {
		return nil
	}
	us.Debug().
		Strs("old", us.current.includes).
		Strs("new", us.next.includes).
		Msg("includes changed")
	return ErrReload
}

//Teardown is the reset method when a change has been detected. Set new state to old state, and reload.
func (us *UsersState) Teardown() error {
	us.current = us.next
	return us.reload()
}

//Register returns a list of files to watch for changes
func (us *UsersState) Register() []string {
	return us.UsersListener.Register(us.current.includes)
}

//Save commits a state to the local DB instance.
func (us *UsersState) Save(db *AgentDB) error {
	us.Debug().Array("users", LogUsers(us.next.users)).Msg("save users")
	return db.SaveUsers(us.next.users)
}

//Load reads in current state from local db instance
func (us *UsersState) Load(db *AgentDB) error {
	users, err := db.LoadUsers()
	if err != nil {
		return err
	}
	us.current = &usersState{users: users}
	return err
}

/* --------------------------------- ACCESS --------------------------------- */

type (
	//AccessState struct keeps track of state changes based on AccessListener struct and methods
	AccessState struct {
		*AccessListener
		current, next Access
	}
)

//Parse calls parse(), and update new AccessState
func (as *AccessState) Parse() (State, error) {
	access, err := as.parse()
	if err != nil {
		return nil, err
	}
	as.next = access
	return as, nil
}

//Changed checks if the new AccessState instance is different from old AccessState instance
func (as *AccessState) Changed() bool {
	add, del := accessDiff(as.current, as.next)
	return !add.IsEmpty() || !del.IsEmpty()
}

//Created checks if the current AccessState has been created
func (as *AccessState) Created() bool { return as.current.IsEmpty() }

//Notify is the method to notify of a change in state
func (as *AccessState) Notify(cmd string) {
	add, del := accessDiff(as.current, as.next)
	as.Warn().
		Object("access", LogAccess(as.next)).
		Object("add", LogAccess(add)).
		Object("del", LogAccess(del)).
		Str("processName", cmd).
		Msg("access entries")
}

//Teardown is the reset method when a change has been detected. Set new state to old state, and reload.
func (as *AccessState) Teardown() error {
	as.current = as.next
	return nil
}

//Save commits a state to the local DB instance.
func (as *AccessState) Save(db *AgentDB) error {
	as.Debug().Object("access", LogAccess(as.next)).Msg("save access")
	return db.SaveAccess(as.next)
}

//Load reads in current state from local db instance
func (as *AccessState) Load(db *AgentDB) (err error) {
	as.current, err = db.LoadAccess()
	return
}

/* ------------------------------ NOP CONSUMER ------------------------------ */
type nopConsumer struct{}

func (np nopConsumer) Register() *sync.Map { return nil }
func (np nopConsumer) Consume(Event) error { return nil }

/* ------------------------------ FILE MISSING ------------------------------ */

//FileMissing struct is used when a watched file cannot be located
type FileMissing struct {
	File string
	Consumer
	zerolog.Logger
}

const pollingDuration = 10 * time.Second

//NewFileMissing function watches for a file to be found, and adds the file to be monitored.
func NewFileMissing(events chan Event, options ...func(*FileMissing)) *FileMissing {
	//NopConsumer is a fake consumer
	var NopConsumer = nopConsumer{}

	fm := &FileMissing{File: "/dev/null", Consumer: NopConsumer, Logger: zerolog.Nop()}
	for _, option := range options {
		option(fm)
	}
	fm.Logger = fm.Logger.With().Str("file", fm.File).
		Str("consumer", "file missing").Logger()
	go fm.start(events)
	return fm
}

func (fm *FileMissing) start(events chan Event) {
	for range time.Tick(pollingDuration) { //nolint
		if _, err := os.Stat(fm.File); err == nil {
			fm.Debug().Msg("file found")
			events <- Event{Path: fm.File, Mode: 1}
			fm.Debug().Msg("pushed to event")
			return
		}
	}
}

//Register method registers the newly found file to the correct consumer
func (fm *FileMissing) Register() *sync.Map {
	out := &sync.Map{}
	out.Store(fm.File, fm)

	if _, err := os.Stat(fm.File); err == nil {
		out = fm.Consumer.Register()
	}
	return out
}
