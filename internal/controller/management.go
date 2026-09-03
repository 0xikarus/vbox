package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// boxInventoryHandler returns logical boxes plus provider services that are not
// managed by the compute fleet. Compute-slot services are intentionally
// omitted: an occupied slot is represented by its logical box, while an idle
// slot is capacity rather than an active box.
func (s *Server) boxInventoryHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	providerName, credential, err := fleetTarget(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	logical, err := s.Store.ListLogicalBoxes(r.Context(), p, providerName, credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	slotServices, err := s.Store.ComputeSlotServiceIDs(r.Context(), p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	boxes, err := prov.List(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	inventory := v1.BoxInventory{LogicalBoxes: logical, ConnectedBoxes: []v1.ConnectedBox{}}
	for _, box := range boxes {
		if slotServices[box.ID] {
			continue
		}
		inventory.ConnectedBoxes = append(inventory.ConnectedBoxes, v1.ConnectedBox{
			ID: box.ID, Name: box.Name, Provider: providerName, ProviderCredential: credential,
			State: box.State, ProviderState: box.ProviderState, Region: box.Region, Image: box.Image,
			Resources: box.Resources, Storage: box.Storage, Management: "external", ChatCapable: false,
		})
	}
	writeJSON(w, http.StatusOK, inventory)
}

func (s *Server) routeBoxMessage(ctx context.Context, p Principal, boxID, idempotency string, request v1.DirectBoxMessageRequest) (v1.DirectBoxMessageResponse, error) {
	var response v1.DirectBoxMessageResponse
	box, err := s.Store.LogicalBox(ctx, p, boxID)
	if err != nil {
		return response, err
	}
	response.BoxState = string(box.State)
	if strings.TrimSpace(request.Text) == "" || len(request.Text) > 100_000 {
		return response, fmt.Errorf("message must contain between 1 and 100000 bytes")
	}
	if idempotency == "" {
		return response, fmt.Errorf("Idempotency-Key is required")
	}
	previousTask, previousMessage, found, err := s.Store.DirectBoxMessageByKey(ctx, p, box.ID, idempotency)
	if err != nil {
		return response, err
	}
	if found {
		response.Task, response.Message = previousTask, previousMessage
		return response, nil
	}
	if request.Agent == "" {
		request.Agent = "claude"
	}
	if request.Session == "" {
		request.Session = "vmbox"
	}
	tasks, err := s.Store.ListBoxTasks(ctx, p, box.ID)
	if err != nil {
		return response, err
	}
	var selected *v1.BoxTask
	for index := len(tasks) - 1; index >= 0; index-- {
		switch tasks[index].State {
		case "active", "starting", "queued", "waiting_capacity":
			task := tasks[index]
			selected = &task
		}
		switch tasks[index].State {
		case "active":
			if box.State == v1.LogicalBoxRunning {
				task := tasks[index]
				selected = &task
			}
		case "starting", "queued", "waiting_capacity":
			task := tasks[index]
			selected = &task
		}
		task, reused, err := s.Store.CreateBoxTask(ctx, p, box.ID, idempotency+":task", v1.CreateBoxTaskRequest{Agent: request.Agent, Session: request.Session, Prompt: request.Text})
		if err != nil {
			return response, err
		}
		messages, err := s.Store.ListBoxMessages(ctx, p, task.ID)
		if err != nil || len(messages) == 0 {
			if err == nil {
				err = fmt.Errorf("new task has no initial message")
			}
			return response, err
		}
		response.Task, response.Message, response.Started = task, messages[0], !reused
		if !reused || task.State == "queued" || task.State == "waiting_capacity" {
			go func() {
				if err := s.executeBoxTask(context.Background(), p.AccountID, task); err != nil {
					s.Logger.Error("direct box message could not start agent", "task", task.ID, "error", err)
				}
			}()
		}
		return response, nil
	}
	message, _, err := s.Store.CreateBoxMessage(ctx, p, selected.ID, idempotency+":message", v1.SendBoxMessageRequest{Text: request.Text})
	if err != nil {
		return response, err
	}
	response.Task, response.Message = *selected, message
	if selected.State == "active" && message.State == "queued" {
		if err := s.deliverBoxMessage(ctx, p, *selected, message, true); err != nil {
			return response, err
		}
		response.Message.State = "delivered"
	}
	return response, nil
}

func (s *Server) directBoxMessageHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.DirectBoxMessageRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	response, err := s.routeBoxMessage(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) chatGroupsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		values, err := s.Store.ListChatGroups(r.Context(), p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
		return
	}
	var request v1.PutChatGroupRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	value, err := s.Store.CreateChatGroup(r.Context(), p, request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (s *Server) chatGroupHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.Store.ChatGroup(r.Context(), p, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodPut:
		var request v1.PutChatGroupRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		value, err := s.Store.UpdateChatGroup(r.Context(), p, r.PathValue("id"), request)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodDelete:
		if err := s.Store.DeleteChatGroup(r.Context(), p, r.PathValue("id")); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) chatGroupMessagesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		values, err := s.Store.ListGroupMessages(r.Context(), p, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
		return
	}
	var request v1.SendGroupMessageRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	message, reused, err := s.Store.CreateGroupMessage(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if !reused {
		go s.dispatchGroupMessage(context.Background(), p, message)
	}
	writeJSON(w, http.StatusAccepted, message)
}

func (s *Server) dispatchGroupMessage(ctx context.Context, p Principal, message v1.GroupMessage) {
	group, err := s.Store.ChatGroup(ctx, p, message.GroupID)
	if err != nil {
		s.Logger.Error("load group for message delivery", "message", message.ID, "error", err)
		return
	}
	agents := make(map[string]string, len(group.Members))
	for _, member := range group.Members {
		agents[member.LogicalBoxID] = member.Agent
	}
	body := "[Group: " + group.Name + "]\n" + message.Text
	if message.SourceBoxName != "" {
		body = "[Forwarded from " + message.SourceBoxName + " in " + group.Name + "]\n" + message.Text
	}
	for _, delivery := range message.Deliveries {
		claimed, err := s.Store.ClaimGroupDelivery(ctx, p.AccountID, message.ID, delivery.LogicalBoxID)
		if err != nil || !claimed {
			continue
		}
		key := "group:" + message.ID + ":" + delivery.LogicalBoxID
		result, routeErr := s.routeBoxMessage(ctx, p, delivery.LogicalBoxID, key, v1.DirectBoxMessageRequest{Text: body, Agent: agents[delivery.LogicalBoxID], Session: "vmbox"})
		state, failure := "delivered", ""
		if routeErr != nil {
			state, failure = "failed", routeErr.Error()
		}
		if err := s.Store.SetGroupDelivery(ctx, p.AccountID, message.ID, delivery.LogicalBoxID, result.Task.ID, result.Message.ID, state, failure); err != nil {
			s.Logger.Error("record group delivery", "message", message.ID, "box", delivery.LogicalBoxID, "error", err)
		}
	}
}

type pendingGroupDelivery struct {
	AccountID string
	Principal Principal
	MessageID string
	BoxID     string
}

func (s *Server) ReconcileGroupDeliveriesNow(ctx context.Context) error {
	values, err := s.Store.PendingGroupDeliveries(ctx)
	if err != nil {
		return err
	}
	for _, value := range values {
		message, err := s.Store.GroupMessage(ctx, value.Principal, value.MessageID)
		if err != nil {
			return err
		}
		s.dispatchGroupMessage(ctx, value.Principal, message)
	}
	return nil
}
