// Package files transfers files and folders between the controller (Client)
// and the host (Server) over one ordered, reliable data channel.
//
// Wire protocol (version 1):
//
//   - Control messages are JSON text messages. Requests from the client carry
//     an "id" and an "op" (hello, home, list, stat, mkdir, download,
//     upload_begin, end, cancel); the server answers {"id":n,"ok":true,...} or
//     {"id":n,"ok":false,"error":code,"detail":text}. Server notifications
//     carry an "op" and a "transfer" (dir, file, end, skip, done, abort).
//   - File content travels in binary messages
//     [1 byte kind][8 byte transfer id][8 byte offset][data], big endian,
//     at most ChunkSize data bytes each. A JSON message longer than
//     maxText is sent as binary kindJSON fragments followed by the final
//     piece as a text message.
//   - Each file ends with {"op":"end","transfer":t,"sha256":hex}; the
//     receiver checks the size and SHA-256, then renames its temporary file
//     (.<name>.mmdesk-part) into place.
package files

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"
)

// Version is the protocol version exchanged in the hello request.
const Version = 1

const (
	// maxMessage is the largest data channel message pion accepts by default.
	maxMessage = 65535
	headerLen  = 17
	// ChunkSize is the number of file bytes per data message (64 KiB less
	// the header and some headroom, to stay under the SCTP message limit).
	ChunkSize = 63 << 10
	// maxText is the largest JSON text message sent in one piece.
	maxText = 60 << 10
	// maxJSON bounds a reassembled fragmented JSON message.
	maxJSON = 32 << 20

	// highWater pauses a sender; lowWater is the OnBufferedAmountLow threshold.
	highWater = 4 << 20
	lowWater  = 1 << 20

	// MaxList is the largest number of entries one List returns.
	MaxList = 5000

	kindData byte = 1
	kindJSON byte = 2

	partSuffix = ".mmdesk-part"
)

// Error codes on the wire.
const (
	codeDenied     = "denied"     // the host turned file access off
	codeNotFound   = "not_found"  //
	codeExists     = "exists"     //
	codeIO         = "io"         //
	codePermission = "permission" // the operating system refused access
	codeInvalid    = "invalid"    // malformed request or path
	codeCanceled   = "canceled"   //
	codeChecksum   = "checksum"   // size or SHA-256 mismatch
	codeVersion    = "version"    //
)

// Errors returned by Client methods and Transfer.Wait; test with errors.Is.
var (
	ErrDenied     = errors.New("files: the other side turned off file access")
	ErrNotFound   = errors.New("files: not found")
	ErrExists     = errors.New("files: already exists")
	ErrIO         = errors.New("files: I/O error")
	ErrPermission = errors.New("files: permission denied by the operating system")
	ErrInvalid    = errors.New("files: invalid request")
	ErrCanceled   = errors.New("files: canceled")
	ErrChecksum   = errors.New("files: size or checksum mismatch")
	ErrVersion    = errors.New("files: incompatible protocol version")
	ErrClosed     = errors.New("files: connection closed")
)

var codeErrs = map[string]error{
	codeDenied: ErrDenied, codeNotFound: ErrNotFound, codeExists: ErrExists, codeIO: ErrIO,
	codePermission: ErrPermission, codeInvalid: ErrInvalid, codeCanceled: ErrCanceled,
	codeChecksum: ErrChecksum, codeVersion: ErrVersion,
}

// RemoteError is an error reported by the other side.
type RemoteError struct {
	Code   string // wire error code
	Detail string // human-readable detail, may be empty
}

func (e *RemoteError) Error() string {
	base := e.Code
	if err, ok := codeErrs[e.Code]; ok {
		base = err.Error()
	}
	if e.Detail != "" {
		return base + ": " + e.Detail
	}
	return base
}

// Is maps the code onto the package's sentinel errors.
func (e *RemoteError) Is(target error) bool { return codeErrs[e.Code] == target }

// errorFor converts a wire code into an error.
func errorFor(code, detail string) error {
	if code == "" {
		code = codeIO
	}
	return &RemoteError{Code: code, Detail: detail}
}

// codeOf classifies a local error into a wire code.
func codeOf(err error) string {
	var re *RemoteError
	switch {
	case errors.As(err, &re):
		return re.Code
	case errors.Is(err, fs.ErrNotExist):
		return codeNotFound
	case errors.Is(err, fs.ErrExist):
		return codeExists
	case errors.Is(err, fs.ErrPermission):
		return codePermission
	case errors.Is(err, ErrDenied):
		return codeDenied
	case errors.Is(err, ErrCanceled):
		return codeCanceled
	case errors.Is(err, ErrChecksum):
		return codeChecksum
	case errors.Is(err, ErrInvalid):
		return codeInvalid
	}
	return codeIO
}

// localError wraps a local error so errors.Is matches the sentinel for its
// code while keeping the original message.
func localError(err error) error {
	if err == nil {
		return nil
	}
	var re *RemoteError
	if errors.As(err, &re) {
		return err
	}
	for _, s := range []error{ErrDenied, ErrCanceled, ErrChecksum, ErrInvalid, ErrClosed} {
		if errors.Is(err, s) {
			return err
		}
	}
	return &RemoteError{Code: codeOf(err), Detail: describe(err)}
}

// describe strips the Go operation prefix from path errors.
func describe(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		if errors.Is(pe.Err, syscall.ENOSPC) {
			return pe.Path + ": disk full"
		}
		return pe.Path + ": " + pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.New + ": " + le.Err.Error()
	}
	return err.Error()
}

// Entry is one file or directory on the host.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"` // absolute path on the side that owns it
	IsDir   bool      `json:"is_dir,omitempty"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mtime"`
	Hidden  bool      `json:"hidden,omitempty"`
	Link    bool      `json:"link,omitempty"` // symbolic link; IsDir and Size describe its target
}

// Listing is the content of one directory.
type Listing struct {
	Path      string  `json:"path"`             // cleaned absolute path
	Parent    string  `json:"parent,omitempty"` // "" at a volume root
	Entries   []Entry `json:"entries"`          // directories first, then by name
	Truncated bool    `json:"truncated,omitempty"`
}

// Home describes the host's file system roots.
type Home struct {
	Dir     string   `json:"dir"`     // user home directory
	Volumes []string `json:"volumes"` // "/" or available drives like `C:\`
	Sep     string   `json:"sep"`     // path separator
	OS      string   `json:"os"`      // runtime.GOOS
}

// wireFailure is one failed file in a folder transfer.
type wireFailure struct {
	Rel    string `json:"rel"`
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// msg is every JSON message; unused fields are omitted.
type msg struct {
	ID     uint64 `json:"id,omitempty"`
	Op     string `json:"op,omitempty"`
	OK     *bool  `json:"ok,omitempty"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`

	Version  int    `json:"version,omitempty"`
	Transfer uint64 `json:"transfer,omitempty"`
	Path     string `json:"path,omitempty"`
	Dir      string `json:"dir,omitempty"`
	Rel      string `json:"rel,omitempty"`
	Name     string `json:"name,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Mode     uint32 `json:"mode,omitempty"`  // permission bits
	MTime    int64  `json:"mtime,omitempty"` // Unix nanoseconds, 0 = unknown
	SHA256   string `json:"sha256,omitempty"`
	Files    int    `json:"files,omitempty"`
	IsDir    bool   `json:"is_dir,omitempty"`
	Unique   bool   `json:"unique,omitempty"` // mkdir: pick a free "name (n)"

	Listing *Listing      `json:"listing,omitempty"`
	Entry   *Entry        `json:"entry,omitempty"`
	Home    *Home         `json:"home,omitempty"`
	Skipped []string      `json:"skipped,omitempty"`
	Failed  []wireFailure `json:"failed,omitempty"`
}

func (m *msg) isResponse() bool { return m.OK != nil }

func okMsg(id uint64) msg {
	t := true
	return msg{ID: id, OK: &t}
}

func errMsg(id uint64, err error) msg {
	f := false
	m := msg{ID: id, OK: &f, Error: codeOf(err)}
	var re *RemoteError
	if errors.As(err, &re) {
		m.Detail = re.Detail
	} else if m.Error != codeDenied {
		m.Detail = describe(err)
	}
	return m
}

func putHeader(b []byte, kind byte, transfer uint64, off int64) {
	b[0] = kind
	binary.BigEndian.PutUint64(b[1:], transfer)
	binary.BigEndian.PutUint64(b[9:], uint64(off))
}

func parseHeader(b []byte) (kind byte, transfer uint64, off int64, data []byte, err error) {
	if len(b) < headerLen {
		return 0, 0, 0, nil, fmt.Errorf("files: short binary message (%d bytes)", len(b))
	}
	return b[0], binary.BigEndian.Uint64(b[1:]), int64(binary.BigEndian.Uint64(b[9:])), b[headerLen:], nil
}
