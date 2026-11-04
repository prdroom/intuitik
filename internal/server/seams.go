package server

import (
	"net/http"
	"time"
)

// Seams are the test inputs of packet section 6.1: clock, port candidates, opener and
// one held response, plus the negative fixtures. The product build passes the zero
// value; only cmd/intuitik built with the e2etest tag fills them
// (TestSeams_ProductBuildHasNone).
type Seams struct {
	Now         func() time.Time       // clock; nil means time.Now
	Ports       []int                  // port candidates, tried before random ports
	BindError   error                  // returned by every bind try instead of binding
	OpenDefault func(url string) error // replaces the OS default opener (BROWSER empty)
	Hold        func(r *http.Request)  // called before a GET / response is written; may block
	Fixture     Fixture
}

// Fixture is one deliberate defect of a negative fixture (section 6.1). Its
// functions are defined only in fixture_e2etest.go; the zero value changes nothing.
type Fixture struct {
	Name            string
	Wrap            func(h http.Handler, port int) http.Handler // runs before Host classification
	TokenLifetime   time.Duration                               // replaces V3 when non-zero
	ExtraRoutes     []Route                                     // appended to the route table
	OnEnter         func(st *state)                             // runs after Enter issued T'
	ClearSiteData   func(sessionIssued bool) (string, bool)     // replaces the S8 (iii) rule
	RefusalBody     func(token string) string                   // replaces the 403 body
	FixedHeaders    func(h http.Header, port int)               // runs after the fixed headers are set
	NoExclusiveBind bool                                        // S16 option positive control
}
