package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type directWorkerConnection struct {
	Worker         DirectWorker
	Peer           *workerprotocol.Peer
	assignmentMu   sync.Mutex
	binding        workerprotocol.Binding
	bindingControl bool
}

func (s *Server) exchangeWorkerEnrollment(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "worker endpoint", http.StatusForbidden)
		return
	}
	var request struct {
		Enrollment string `json:"enrollmentToken"`
		Credential string `json:"credential"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid enrollment request", 400)
		return
	}
	worker, err := s.Store.ExchangeWorkerEnrollment(r.Context(), request.Enrollment, request.Credential)
	if err != nil {
		http.Error(w, "invalid or expired enrollment", 401)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, worker)
}

func (s *Server) connectDirectWorker(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "worker endpoint", http.StatusForbidden)
		return
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		http.Error(w, "worker authentication required", 401)
		return
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	worker, err := s.Store.AuthenticateWorker(r.Context(), token)
	if err != nil {
		http.Error(w, "invalid worker credential", 401)
		return
	}
	s.mu.Lock()
	if s.directWorkerOwner == "" {
		s.directWorkerOwner, err = secretToken()
	}
	owner := s.directWorkerOwner
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "worker service unavailable", 503)
		return
	}
	if err = s.Store.ClaimWorkerController(r.Context(), owner); err != nil {
		http.Error(w, "direct workers require a single active controller", 503)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8192)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	handshake, done := context.WithTimeout(ctx, 10*time.Second)
	var hello workerprotocol.Hello
	err = wsjson.Read(handshake, conn, &hello)
	bindingControl := slices.Contains(hello.Capabilities, "binding-v1")
	validBinding := !bindingControl || hello.Binding != nil &&
		hello.Binding.AccountID == worker.AccountID && hello.Binding.SlotID == worker.SlotID &&
		hello.Binding.Incarnation == hello.Incarnation && hello.Binding.BoxID != "" && hello.Binding.Assignment != ""
	if err == nil && hello.Version == workerprotocol.Version && len(hello.Capabilities) <= 32 && validBinding {
		worker, err = s.Store.ClaimWorkerConnection(handshake, token, hello.Incarnation, owner)
	} else {
		done()
		return
	}
	if err != nil {
		done()
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.ReleaseWorkerConnection(ctx, worker, owner)
	}()
	err = wsjson.Write(handshake, conn, workerprotocol.Welcome{Capabilities: []string{"binding-v1", "observations-v1"}, Version: workerprotocol.Version, WorkerID: worker.ID, AccountID: worker.AccountID, SlotID: worker.SlotID, Epoch: worker.Epoch})
	done()
	if err != nil {
		return
	}
	peer := workerprotocol.New(ctx, conn, true)
	defer peer.Close()
	connection := &directWorkerConnection{Worker: worker, Peer: peer, bindingControl: bindingControl}
	if bindingControl {
		connection.binding = *hello.Binding
	}
	s.mu.Lock()
	if s.directWorkers == nil {
		s.directWorkers = make(map[string]*directWorkerConnection)
	}
	old := s.directWorkers[worker.ID]
	if old != nil && old.Worker.Epoch >= worker.Epoch {
		s.mu.Unlock()
		return
	}
	s.directWorkers[worker.ID] = connection
	s.mu.Unlock()
	if old != nil {
		old.Peer.Close()
	}
	defer func() {
		s.mu.Lock()
		if s.directWorkers[worker.ID] == connection {
			delete(s.directWorkers, worker.ID)
		}
		s.mu.Unlock()
	}()
	// Workers must not open arbitrary execution requests on the controller.
	go func() {
		if stream, err := peer.Accept(ctx); err == nil {
			stream.Close()
			peer.Close()
		}
	}()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-peer.Done():
			return
		case <-ctx.Done():
			return
		case observation := <-peer.Observations():
			if !slices.Contains(hello.Capabilities, "observations-v1") || observation.Binding.AccountID != worker.AccountID || observation.Binding.SlotID != worker.SlotID || observation.Binding.Incarnation != worker.Incarnation {
				return
			}
			recordCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			recordErr := s.Store.RecordWorkerObservation(recordCtx, worker, owner, observation)
			stop()
			// A single failed observation write is never evidence that the worker
			// changed. Dropping one snapshot keeps live viewer streams up through
			// transient store stalls; the next 20-second observation retries, and
			// a real takeover or revocation is enforced by the renewal below and
			// the per-stream fences.
			if recordErr != nil && lostWorkerLease(recordErr) {
				return
			}
			if recordErr != nil {
				s.Logger.Error("worker observation recording failed; keeping connection", "worker", worker.ID, "error", recordErr)
			}
		case <-ticker.C:
			renew, done := context.WithTimeout(ctx, 5*time.Second)
			err = s.Store.ClaimWorkerController(renew, owner)
			if err == nil {
				err = s.Store.RenewWorkerConnection(renew, worker, owner, nil)
			}
			done()
			if err != nil {
				// Transient store failures must not close the shared worker
				// connection: that would disconnect every terminal and desktop
				// viewer at once. The 60-second connection lease bounds staleness,
				// and a lost lease or revoked worker fails closed immediately.
				if lostWorkerLease(err) {
					return
				}
				s.Logger.Error("worker connection renewal failed; keeping connection", "worker", worker.ID, "error", err)
			}
		}
	}
}
