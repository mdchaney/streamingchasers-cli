package api

import "sync"

// SpecVersion is the info.version of the OpenAPI description this
// program was built against.  It is sent with every request, as
// X-Client-API-Version, and the server says in answer whether it speaks
// the same version, a newer one or an older one.
//
// A release build sets it from the description itself:
//
//	go build -ldflags "-X github.com/mdchaney/streamingchasers-cli/internal/api.SpecVersion=1.2.0"
var SpecVersion = "1.1.0"

// What the server may say of the version a client declares.
const (
	// StatusCurrent: the client was built against the server's version.
	StatusCurrent = "current"
	// StatusOutdated: the server is newer; the client should be upgraded.
	StatusOutdated = "outdated"
	// StatusAhead: the client is newer than the server, which has not
	// been deployed with what the client was built against.
	StatusAhead = "ahead"
	// StatusUnknown: what the client sent was not a version.
	StatusUnknown = "unknown"
)

// Versions remembers what a server last said of its API version.  One
// may be shared by several clients.
type Versions struct {
	mu     sync.Mutex
	server string
	status string
}

// Seen records the version headers of a response.  A response without
// them, such as one from a proxy, changes nothing.
func (v *Versions) Seen(server, status string) {
	if v == nil || server == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.server = server
	if status != "" {
		v.status = status
	}
}

// Server is the API version the server speaks, or "" if it has not said.
func (v *Versions) Server() string {
	if v == nil {
		return ""
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.server
}

// Status is what the server made of the client's version, or "".
func (v *Versions) Status() string {
	if v == nil {
		return ""
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.status
}
