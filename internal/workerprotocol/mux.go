// Package workerprotocol carries bounded, multiplexed byte streams between a
// controller and a worker. Authentication and assignment validation precede Open.
package workerprotocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const Version = 1
const chunkSize = 32 * 1024
const window = 8
const maxStreams = 32

// Binding is checked on both sides before executing a command. An incarnation
// identifies an agent process; reconnecting its socket does not change it.
type Binding struct {
	AccountID   string `json:"accountId"`
	BoxID       string `json:"boxId"`
	SlotID      string `json:"slotId"`
	Assignment  string `json:"assignment"`
	Incarnation string `json:"incarnation"`
}

type Request struct {
	Binding     Binding  `json:"binding"`
	OperationID string   `json:"operationId"`
	Argv        []string `json:"argv"`
	Rebind      *Binding `json:"rebind,omitempty"`
}

// Control and execution are mutually exclusive; a binding update never executes
// command arguments. Only the authenticated controller-agent channel carries it.
func (r Request) valid() bool {
	return r.OperationID != "" && ((r.Rebind == nil && len(r.Argv) > 0) || (r.Rebind != nil && len(r.Argv) == 0))
}

type frame struct {
	Observation *Observation `json:"observation,omitempty"`
	Kind        string       `json:"kind"`
	ID          uint64       `json:"id,omitempty"`
	Request     *Request     `json:"request,omitempty"`
	Data        []byte       `json:"data,omitempty"`
}

// Peer has exactly one socket reader and writer. Each stream receives at most
// window chunks until its consumer returns credits. Control traffic has a
// separate bounded queue and takes priority over bulk output.
type Peer struct {
	conn            *websocket.Conn
	ctx             context.Context
	cancel          context.CancelFunc
	control         chan frame
	data            chan frame
	incoming        chan *Stream
	mu              sync.Mutex
	streams         map[uint64]*Stream
	next            atomic.Uint64
	lastRemote      uint64 // read-loop owned
	lastObservation uint64 // read-loop owned
	observations    chan Observation
	lastSeen        atomic.Int64
	odd             bool
	err             error
	done            chan struct{}
}

func New(ctx context.Context, conn *websocket.Conn, initiator bool) *Peer {
	ctx, cancel := context.WithCancel(ctx)
	p := &Peer{conn: conn, ctx: ctx, cancel: cancel, control: make(chan frame, 256), data: make(chan frame, 32), incoming: make(chan *Stream, maxStreams), observations: make(chan Observation, 1), streams: map[uint64]*Stream{}, odd: initiator, done: make(chan struct{})}
	if initiator {
		p.next.Store(1)
	} else {
		p.next.Store(2)
	}
	p.lastSeen.Store(time.Now().UnixNano())
	conn.SetReadLimit(64 * 1024)
	go p.writeLoop()
	go p.readLoop()
	go p.heartbeatLoop()
	go func() { <-ctx.Done(); conn.CloseNow() }()
	return p
}

func (p *Peer) Done() <-chan struct{} { return p.done }
func (p *Peer) Err() error            { p.mu.Lock(); defer p.mu.Unlock(); return p.err }
func (p *Peer) Close() error          { p.cancel(); return p.conn.CloseNow() }

func (p *Peer) send(ctx context.Context, f frame, control bool) error {
	queue := p.data
	if control {
		queue = p.control
	}
	select {
	case <-p.ctx.Done():
		return io.ErrClosedPipe
	case <-ctx.Done():
		return ctx.Err()
	case queue <- f:
		return nil
	}
}

// sendControl never blocks the reader behind an application stream. Exhausting
// the control budget is a protocol failure, rather than unbounded allocation.
func (p *Peer) sendControl(f frame) error {
	select {
	case p.control <- f:
		return nil
	default:
		return errors.New("worker control queue exhausted")
	}
}

func (p *Peer) Open(ctx context.Context, req Request) (*Stream, error) {
	if !req.valid() {
		return nil, errors.New("command and operation ID required")
	}
	id := p.next.Add(2) - 2
	s, err := p.add(id, req)
	if err != nil {
		return nil, err
	}
	if err = p.send(ctx, frame{Kind: "open", ID: id, Request: &req}, true); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (p *Peer) Accept(ctx context.Context) (*Stream, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ctx.Done():
		return nil, io.ErrClosedPipe
	case s := <-p.incoming:
		return s, nil
	}
}
func (p *Peer) add(id uint64, req Request) (*Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.streams) >= maxStreams {
		return nil, errors.New("worker stream capacity reached")
	}
	if _, exists := p.streams[id]; exists {
		return nil, errors.New("duplicate worker stream")
	}
	s := &Stream{peer: p, ID: id, Request: req, input: make(chan []byte, window+1), credits: make(chan struct{}, window), closed: make(chan struct{})}
	for range window {
		s.credits <- struct{}{}
	}
	p.streams[id] = s
	return s, nil
}
func (p *Peer) writeLoop() {
	defer p.cancel()
	for {
		var f frame
		select {
		case f = <-p.control:
		default:
			select {
			case <-p.ctx.Done():
				return
			case f = <-p.control:
			case f = <-p.data:
			}
		}
		data, err := json.Marshal(f)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
		err = p.conn.Write(ctx, websocket.MessageText, data)
		cancel()
		if err != nil {
			return
		}
	}
}
func (p *Peer) readLoop() {
	defer close(p.done)
	defer p.cancel()
	for {
		kind, data, err := p.conn.Read(p.ctx)
		if err == nil && kind != websocket.MessageText {
			err = errors.New("invalid worker frame type")
		}
		var f frame
		if err == nil {
			err = json.Unmarshal(data, &f)
		}
		if err == nil {
			err = p.receive(f)
		}
		if err == nil {
			p.lastSeen.Store(time.Now().UnixNano())
		}
		if err != nil {
			p.mu.Lock()
			p.err = err
			p.mu.Unlock()
			return
		}
	}
}
func (p *Peer) receive(f frame) error {
	if f.Kind == "observation" {
		if !p.odd || f.ID != 0 || f.Observation == nil || !f.Observation.valid() || f.Observation.Sequence <= p.lastObservation {
			return errors.New("invalid worker observation sequence or direction")
		}
		p.lastObservation = f.Observation.Sequence
		// Keep only the latest snapshot; a slow consumer cannot grow memory or
		// block the socket reader and unrelated command streams.
		select {
		case <-p.observations:
		default:
		}
		p.observations <- *f.Observation
		return nil
	}

	if f.Kind == "ping" {
		return p.sendControl(frame{Kind: "pong"})
	}
	if f.Kind == "pong" {
		return nil
	}
	if f.ID == 0 {
		return errors.New("missing worker stream ID")
	}
	if f.Kind == "open" {
		if f.ID <= p.lastRemote || (f.ID%2 == 1) == p.odd || f.Request == nil || !f.Request.valid() {
			return errors.New("invalid worker open")
		}
		p.lastRemote = f.ID
		s, err := p.add(f.ID, *f.Request)
		if err != nil {
			return err
		}
		select {
		case p.incoming <- s:
			return nil
		default:
			return errors.New("worker accept queue exhausted")
		}
	}
	p.mu.Lock()
	s := p.streams[f.ID]
	p.mu.Unlock()
	// Late data/credits after local cancellation are harmless and never reopen it.
	if s == nil {
		return nil
	}
	switch f.Kind {
	case "data":
		if len(f.Data) == 0 || len(f.Data) > chunkSize {
			return errors.New("invalid worker data size")
		}
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		if s.eof {
			return errors.New("worker data after EOF")
		}
		if s.outstanding >= window {
			return errors.New("worker stream exceeded receive window")
		}
		s.outstanding++
		select {
		case s.input <- f.Data:
			return nil
		default:
			return errors.New("worker receive queue exhausted")
		}
	case "credit":
		select {
		case s.credits <- struct{}{}:
			return nil
		default:
			return errors.New("invalid worker stream credit")
		}
	case "eof":
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		if s.eof {
			return errors.New("duplicate worker EOF")
		}
		s.eof = true
		select {
		case s.input <- nil:
			return nil
		default:
			return errors.New("worker EOF queue exhausted")
		}
	case "close":
		s.closeLocal()
		return nil
	default:
		return fmt.Errorf("unknown worker frame %q", f.Kind)
	}
}

// Stream is a bidirectional stream with independent EOF in each direction.
// Close cancels the viewer/command stream; it is not a logical-box stop request.
type Stream struct {
	peer        *Peer
	ID          uint64
	Request     Request
	input       chan []byte
	credits     chan struct{}
	closed      chan struct{}
	once        sync.Once
	readMu      sync.Mutex
	writeMu     sync.Mutex
	stateMu     sync.Mutex
	outstanding int
	eof         bool
	readEOF     bool
	writeEOF    bool
	pending     []byte
}

func (s *Stream) Read(b []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	if s.readEOF {
		return 0, io.EOF
	}
	if len(s.pending) == 0 {
		select {
		case <-s.peer.ctx.Done():
			return 0, io.ErrUnexpectedEOF
		case <-s.closed:
			return 0, io.ErrClosedPipe
		case s.pending = <-s.input:
		}
		if s.pending == nil {
			s.readEOF = true
			return 0, io.EOF
		}
	}
	n := copy(b, s.pending)
	s.pending = s.pending[n:]
	if len(s.pending) == 0 {
		s.stateMu.Lock()
		s.outstanding--
		s.stateMu.Unlock()
		if err := s.peer.sendControl(frame{Kind: "credit", ID: s.ID}); err != nil {
			s.peer.cancel()
			return n, err
		}
	}
	return n, nil
}
func (s *Stream) Write(b []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeEOF {
		return 0, io.ErrClosedPipe
	}
	total := 0
	for len(b) > 0 {
		select {
		case <-s.peer.ctx.Done():
			return total, io.ErrClosedPipe
		case <-s.closed:
			return total, io.ErrClosedPipe
		case <-s.credits:
		}
		n := min(len(b), chunkSize)
		data := append([]byte(nil), b[:n]...)
		if err := s.sendData(frame{Kind: "data", ID: s.ID, Data: data}); err != nil {
			return total, err
		}
		total += n
		b = b[n:]
	}
	return total, nil
}

// Done reports full stream closure, separately from the stdin half-close.
func (s *Stream) Done() <-chan struct{} { return s.closed }

func (s *Stream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeEOF {
		return nil
	}
	s.writeEOF = true
	// EOF uses the data queue so it cannot overtake preceding bytes.
	return s.sendData(frame{Kind: "eof", ID: s.ID})
}
func (s *Stream) closeLocal() {
	s.once.Do(func() { close(s.closed); s.peer.mu.Lock(); delete(s.peer.streams, s.ID); s.peer.mu.Unlock() })
}
func (s *Stream) Close() error {
	s.closeLocal()
	return s.peer.sendControl(frame{Kind: "close", ID: s.ID})
}

func (s *Stream) sendData(f frame) error {
	select {
	case <-s.closed:
		return io.ErrClosedPipe
	case <-s.peer.ctx.Done():
		return io.ErrClosedPipe
	case s.peer.data <- f:
		return nil
	}
}

func (p *Peer) heartbeatLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, p.lastSeen.Load())) >= 60*time.Second {
				p.cancel()
				return
			}
			if err := p.sendControl(frame{Kind: "ping"}); err != nil {
				p.cancel()
				return
			}
		}
	}
}
